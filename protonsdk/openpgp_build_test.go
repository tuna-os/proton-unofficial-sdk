package protonsdk

import (
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// go.mod asks for the "-proton" builds of gopenpgp and go-crypto, the ones
// go-proton-api is written against. They stop unpacking a compressed message
// at 50 MiB when the caller sets no limit, and gopenpgp sets none. The plain
// builds of the same version numbers have no limit, so a few hundred
// kilobytes of mail would unpack until memory runs out. The plain versions
// sort higher, which is how an update tool takes them for an upgrade.
func TestOpenPGPBuildCapsDecompression(t *testing.T) {
	var config *packet.Config
	limit := config.DecompressedMessageSizeLimit()
	if limit == nil {
		t.Fatal("go-crypto reads compressed messages without a size limit: go.mod is not on the -proton builds")
	}
	if *limit <= 0 {
		t.Fatalf("decompression limit = %d, want a positive size", *limit)
	}
}
