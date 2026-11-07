package org.tunaos.proton

import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/**
 * A resumable Proton session: the `session` object of the JSON contract,
 * kept as its JSON text. It is as powerful as the account's password (it
 * holds the tokens and the salted key passphrase), so store it encrypted,
 * for example in the Android Keystore, and never log it; [toString] is
 * redacted.
 *
 * Proton rotates the refresh token whenever the access token is refreshed,
 * and the old one stops working. [ProtonAccount] therefore hands every new
 * session to its [ProtonSessionStore] before it returns or throws.
 */
@JvmInline
public value class ProtonSession(public val json: String) {
    /** The address the session signed in with. */
    public val login: String get() = field("login")

    /** Proton's user id. */
    public val userId: String get() = field("user_id")

    internal fun toJson(): JsonObject = protonJson.parseToJsonElement(json).jsonObject

    private fun field(name: String): String = toJson()[name]?.jsonPrimitive?.content.orEmpty()

    override fun toString(): String = "ProtonSession(<redacted>)"
}

/**
 * Where an account's session lives between runs. [save] is awaited after
 * every request that rotated or carried the session, before the request's
 * result is returned or its error thrown; if [save] throws, that exception
 * propagates instead and the result is dropped, so nothing acts on a reply
 * whose session was not persisted.
 */
public fun interface ProtonSessionStore {
    public suspend fun save(session: ProtonSession)
}
