package org.tunaos.proton

/** The contract's fixed error vocabulary; [UNKNOWN] is a code this wrapper predates. */
public enum class ProtonErrorCode(public val wire: String) {
    INVALID_REQUEST("invalid_request"),
    UNSUPPORTED_VERSION("unsupported_version"),
    HELPER_FAILED("helper_failed"),
    LOGIN_FAILED("login_failed"),
    SECOND_FACTOR_REQUIRED("second_factor_required"),
    INVALID_TOTP("invalid_totp"),
    UNSUPPORTED_SECOND_FACTOR("unsupported_second_factor"),
    SECOND_FACTOR_FAILED("second_factor_failed"),
    MAILBOX_PASSWORD_REQUIRED("mailbox_password_required"),
    MAILBOX_PASSWORD_FAILED("mailbox_password_failed"),
    HUMAN_VERIFICATION_REQUIRED("human_verification_required"),
    HUMAN_VERIFICATION_FAILED("human_verification_failed"),
    CLIENT_OUTDATED("client_outdated"),
    SESSION_INVALID("session_invalid"),
    SESSION_REVOKED("session_revoked"),
    REVOKE_FAILED("revoke_failed"),
    TRANSPORT_FAILED("transport_failed"),
    TIMEOUT("timeout"),
    REQUEST_FAILED("request_failed"),
    INVALID_ID("invalid_id"),
    INVALID_PAGE("invalid_page"),
    INVALID_BATCH("invalid_batch"),
    FOLDER_NOT_FOUND("folder_not_found"),
    INVALID_DESTINATION("invalid_destination"),
    DECRYPTION_FAILED("decryption_failed"),
    ATTACHMENT_FAILED("attachment_failed"),
    INVALID_RECIPIENT("invalid_recipient"),
    SENDER_UNKNOWN("sender_unknown"),
    ORDINARY_DELIVERY_REQUIRES_CONSENT("ordinary_delivery_requires_consent"),
    CONFIDENTIAL_SEND_UNSUPPORTED("confidential_send_unsupported"),
    MESSAGE_TOO_LARGE("message_too_large"),
    SEND_FAILED("send_failed"),
    SEND_UNCERTAIN("send_uncertain"),

    /** Per-id outcome only: an earlier id's outage stopped the batch. */
    NOT_ATTEMPTED("not_attempted"),
    UNKNOWN(""),
    ;

    public companion object {
        private val byWire = entries.associateBy { it.wire }

        public fun fromWire(wire: String): ProtonErrorCode = byWire[wire]?.takeIf { it != UNKNOWN } ?: UNKNOWN
    }
}

/**
 * A request the library refused or could not complete. [wire] is the code
 * exactly as received; the message is that code and never server text.
 */
public sealed class ProtonException(public val wire: String) : Exception(wire) {
    public val code: ProtonErrorCode get() = ProtonErrorCode.fromWire(wire)

    /** Credentials, second factor, mailbox password or client version refused. */
    public class Auth internal constructor(wire: String) : ProtonException(wire)

    /**
     * Proton wants a CAPTCHA (or another check) before signing in. Show
     * [challenge] in a WebView (see [HumanVerificationChallenge.verifyUrl])
     * and retry the login with the solved token. A null challenge cannot be
     * solved from this reply.
     */
    public class HumanVerificationRequired internal constructor(
        public val challenge: HumanVerificationChallenge?,
    ) : ProtonException(ProtonErrorCode.HUMAN_VERIFICATION_REQUIRED.wire)

    /** The stored session is unusable or was ended: sign in again. */
    public class Session internal constructor(wire: String) : ProtonException(wire)

    /** Network failure, throttling, server error or the 90-second budget. */
    public class Transport internal constructor(wire: String) : ProtonException(wire)

    /** The request itself is malformed: ids, pages, batches, version. */
    public class InvalidRequest internal constructor(wire: String) : ProtonException(wire)

    /** The folder does not exist or mail cannot be moved there. */
    public class Folder internal constructor(wire: String) : ProtonException(wire)

    /** A message or attachment cannot be decrypted, fetched or uploaded. */
    public class Content internal constructor(wire: String) : ProtonException(wire)

    /** A send was refused before anything went out: safe to fix and retry. */
    public class Send internal constructor(wire: String) : ProtonException(wire)

    /** The submit may have delivered. Never retry this send automatically. */
    public class SendUncertain internal constructor() : ProtonException(ProtonErrorCode.SEND_UNCERTAIN.wire)

    /** The server refused for a reason with no code of its own, or the library failed. */
    public class Failed internal constructor(wire: String) : ProtonException(wire)

    internal companion object {
        fun of(wire: String, challenge: HumanVerificationChallenge? = null): ProtonException =
            when (ProtonErrorCode.fromWire(wire)) {
                ProtonErrorCode.LOGIN_FAILED,
                ProtonErrorCode.SECOND_FACTOR_REQUIRED,
                ProtonErrorCode.INVALID_TOTP,
                ProtonErrorCode.UNSUPPORTED_SECOND_FACTOR,
                ProtonErrorCode.SECOND_FACTOR_FAILED,
                ProtonErrorCode.MAILBOX_PASSWORD_REQUIRED,
                ProtonErrorCode.MAILBOX_PASSWORD_FAILED,
                ProtonErrorCode.HUMAN_VERIFICATION_FAILED,
                ProtonErrorCode.CLIENT_OUTDATED,
                -> Auth(wire)
                ProtonErrorCode.HUMAN_VERIFICATION_REQUIRED -> HumanVerificationRequired(challenge)
                ProtonErrorCode.SESSION_INVALID,
                ProtonErrorCode.SESSION_REVOKED,
                ProtonErrorCode.REVOKE_FAILED,
                -> Session(wire)
                ProtonErrorCode.TRANSPORT_FAILED,
                ProtonErrorCode.TIMEOUT,
                -> Transport(wire)
                ProtonErrorCode.INVALID_REQUEST,
                ProtonErrorCode.UNSUPPORTED_VERSION,
                ProtonErrorCode.INVALID_ID,
                ProtonErrorCode.INVALID_PAGE,
                ProtonErrorCode.INVALID_BATCH,
                -> InvalidRequest(wire)
                ProtonErrorCode.FOLDER_NOT_FOUND,
                ProtonErrorCode.INVALID_DESTINATION,
                -> Folder(wire)
                ProtonErrorCode.DECRYPTION_FAILED,
                ProtonErrorCode.ATTACHMENT_FAILED,
                -> Content(wire)
                ProtonErrorCode.INVALID_RECIPIENT,
                ProtonErrorCode.SENDER_UNKNOWN,
                ProtonErrorCode.ORDINARY_DELIVERY_REQUIRES_CONSENT,
                ProtonErrorCode.CONFIDENTIAL_SEND_UNSUPPORTED,
                ProtonErrorCode.MESSAGE_TOO_LARGE,
                ProtonErrorCode.SEND_FAILED,
                -> Send(wire)
                ProtonErrorCode.SEND_UNCERTAIN -> SendUncertain()
                ProtonErrorCode.HELPER_FAILED,
                ProtonErrorCode.REQUEST_FAILED,
                ProtonErrorCode.NOT_ATTEMPTED,
                ProtonErrorCode.UNKNOWN,
                -> Failed(wire)
            }
    }
}
