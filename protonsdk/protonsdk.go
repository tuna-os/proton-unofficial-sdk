// Package protonsdk is an unofficial Proton Mail client: SRP login with
// TOTP and human verification, folder/label sync with event-based
// increments, decrypted RFC 5322 messages, read and star state, moves,
// end-to-end encrypted sending and server-side logout, all over Proton's
// own go-proton-api and gopenpgp.
//
// The whole API is one JSON request in, one versioned JSON envelope out:
// {"version":1,"result":{...}} or {"version":1,"error":{"code":"..."}}.
// Request takes and returns strings so gomobile can bind it for Android;
// RequestBytes avoids the string copies for Go callers; Serve is the
// stdin/stdout framing the proton-helper binary uses. The contract is
// documented in the repository's README.
//
// Nothing is logged anywhere: requests carry passwords and session
// material, so the libraries' own loggers are silenced on first use, a
// library panic becomes helper_failed, and replies never carry server text.
package protonsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// ProtocolVersion is the envelope version every request and reply carries.
const ProtocolVersion = 1

// MaxRequestBytes caps one request. Attachments ride inside the request
// (base64), so the cap covers a large mail, not just a JSON envelope — the
// Tuta helper's figure.
const MaxRequestBytes = 32 * 1024 * 1024

// Every operation runs under this budget; a caller running the helper as a
// process allows a little more for startup (Hylki allows 100 seconds).
const operationTimeout = 90 * time.Second

// Best-effort cleanup (closing a half-made login, deleting an unsent draft)
// gets its own short budget: it must run even when the operation's own
// deadline is what failed it.
const abandonTimeout = 10 * time.Second

type request struct {
	Version *int   `json:"version"`
	Op      string `json:"op"`
}

// envelope is the single reply line. A session-bearing operation that
// failed after the server rotated its tokens still hands the new session
// back inside the error, so the caller never keeps a dead refresh token.
type envelope struct {
	Version int `json:"version"`
	// any, not map: omitempty drops an empty map, but revoke's result is
	// exactly {} and must still be present.
	Result any         `json:"result,omitempty"`
	Error  *codedError `json:"error,omitempty"`
}

type codedError struct {
	Code    string          `json:"code"`
	Session *sessionPayload `json:"session,omitempty"`
	// HV is the challenge of a human_verification_required login refusal.
	HV *hvChallenge `json:"hv,omitempty"`
}

func (e *codedError) Error() string { return e.Code }

type handler func(ctx context.Context, raw []byte) envelope

var operations = map[string]handler{
	"login":         login,
	"sync":          syncOp,
	"events":        eventsOp,
	"message":       messageOp,
	"mark_read":     markReadOp,
	"move":          moveOp,
	"star":          starOp,
	"label":         labelOp,
	"delete":        deleteOp,
	"empty_folder":  emptyFolderOp,
	"send":          sendOp,
	"save_draft":    saveDraftOp,
	"create_folder": createFolderOp,
	"rename_folder": renameFolderOp,
	"delete_folder": deleteFolderOp,
	"revoke":        revokeOp,
}

// Request runs one JSON request and returns its reply envelope, without a
// trailing newline. It never fails: anything that goes wrong is an error
// envelope. Safe for concurrent use, but two requests carrying the same
// session race on its refresh token (see the README's rotation rules).
func Request(request string) string {
	return string(RequestBytes([]byte(request)))
}

// RequestBytes is Request over bytes, for callers that already hold the
// request as bytes (a large send is mostly base64 attachment data).
func RequestBytes(request []byte) []byte {
	silenceLibraries()
	if len(request) > MaxRequestBytes {
		return encode(failure("invalid_request"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cancel()
	return encode(dispatch(ctx, bytes.TrimSpace(request)))
}

// Serve is the helper's framing: one request read from stdin up to EOF, one
// reply line written to stdout.
func Serve(stdin io.Reader, stdout io.Writer) {
	input, err := io.ReadAll(io.LimitReader(stdin, MaxRequestBytes+1))
	if err != nil {
		write(stdout, failure("invalid_request"))
		return
	}
	stdout.Write(append(RequestBytes(input), '\n'))
}

var silence sync.Once

// silenceLibraries discards logrus output: go-proton-api logs key ids and
// request URLs through it, and nothing it says is wanted. This is
// process-wide; a Go program embedding the package loses its own logrus
// output too, which is the price of never leaking request data.
func silenceLibraries() {
	silence.Do(func() { logrus.SetOutput(io.Discard) })
}

func dispatch(ctx context.Context, raw []byte) (reply envelope) {
	var decoded request
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return failure("invalid_request")
	}
	if decoded.Version == nil {
		return failure("invalid_request")
	}
	if *decoded.Version != ProtocolVersion {
		return failure("unsupported_version")
	}
	run, ok := operations[decoded.Op]
	if !ok {
		return failure("invalid_request")
	}
	// A library panic must still produce exactly one envelope, and its
	// value — which could quote request data — is never printed.
	defer func() {
		if recover() != nil {
			reply = failure("helper_failed")
		}
	}()
	return run(ctx, raw)
}

func failure(code string) envelope {
	return envelope{Version: ProtocolVersion, Error: &codedError{Code: code}}
}

func success(result map[string]any) envelope {
	return envelope{Version: ProtocolVersion, Result: result}
}

func encode(response envelope) []byte {
	encoded, err := json.Marshal(response)
	if err != nil {
		return []byte(`{"version":1,"error":{"code":"helper_failed"}}`)
	}
	return encoded
}

func write(out io.Writer, response envelope) {
	out.Write(append(encode(response), '\n'))
}
