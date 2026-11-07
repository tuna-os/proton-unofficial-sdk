package org.tunaos.proton

import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

/**
 * Entry point: signs in, or resumes a stored session, and hands back a
 * [ProtonAccount] that owns the session from then on.
 *
 * Requests block in Go for up to 90 seconds, so they run on [dispatcher].
 */
public class ProtonClient(
    private val transport: ProtonTransport = GoProtonTransport,
    private val dispatcher: CoroutineDispatcher = Dispatchers.IO,
) {
    /**
     * Signs in with SRP, completing a TOTP challenge and unlocking the keys.
     * The new session is saved to [store] before the account is returned.
     *
     * @throws ProtonException.HumanVerificationRequired when Proton wants a
     *   CAPTCHA first; retry with [humanVerification] once it is solved.
     * @throws ProtonException.Auth for refused credentials or factors.
     */
    public suspend fun login(
        email: String,
        password: String,
        store: ProtonSessionStore,
        totp: String? = null,
        mailboxPassword: String? = null,
        humanVerification: HumanVerificationToken? = null,
    ): ProtonAccount {
        val request = buildJsonObject {
            put("version", PROTOCOL_VERSION)
            put("op", "login")
            put("email", email)
            put("password", password)
            totp?.let { put("totp", it) }
            mailboxPassword?.let { put("mailbox_password", it) }
            humanVerification?.let {
                put("hv_token", it.token)
                put("hv_type", it.type)
            }
        }
        val envelope = exchange(request, store)
        envelope.resultOrThrow()
        val session = envelope.session ?: throw ProtonException.of(ProtonErrorCode.HELPER_FAILED.wire)
        return ProtonAccount(session, store, this)
    }

    /** Resumes a stored session; nothing is contacted until the first request. */
    public fun account(session: ProtonSession, store: ProtonSessionStore): ProtonAccount =
        ProtonAccount(session, store, this)

    /**
     * Runs [request] and saves the session its reply carries, both shielded
     * from cancellation: the Go call cannot be interrupted, and a reply
     * whose rotated session was dropped would sign the account out.
     */
    internal suspend fun exchange(request: JsonObject, store: ProtonSessionStore, onSession: (ProtonSession) -> Unit = {}): Envelope =
        withContext(NonCancellable + dispatcher) {
            val envelope = Envelope.parse(transport.request(request.toString()))
            envelope.session?.let { session ->
                onSession(session)
                store.save(session)
            }
            envelope
        }
}
