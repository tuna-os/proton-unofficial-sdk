package org.tunaos.proton

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.decodeFromJsonElement
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/** The envelope version this wrapper speaks. */
internal const val PROTOCOL_VERSION = 1

internal val protonJson = Json {
    ignoreUnknownKeys = true
    coerceInputValues = true
    explicitNulls = false
}

/**
 * One parsed reply. [session] is the session as it stands after the
 * request, from `result.session` or `error.session`, whichever the reply
 * carries; [failure] is set for an error envelope, [result] otherwise.
 */
internal class Envelope(
    val result: JsonObject?,
    val failure: ProtonException?,
    val session: ProtonSession?,
) {
    fun resultOrThrow(): JsonObject = failure?.let { throw it } ?: result!!

    companion object {
        fun parse(reply: String): Envelope {
            val root = runCatching { protonJson.parseToJsonElement(reply).jsonObject }
                .getOrElse { throw ProtonException.of(ProtonErrorCode.HELPER_FAILED.wire) }
            if (root["version"]?.jsonPrimitive?.intOrNull != PROTOCOL_VERSION) {
                throw ProtonException.of(ProtonErrorCode.UNSUPPORTED_VERSION.wire)
            }
            val error = root["error"] as? JsonObject
            if (error != null) {
                val wire = error["code"]?.jsonPrimitive?.content ?: ProtonErrorCode.HELPER_FAILED.wire
                val challenge = (error["hv"] as? JsonObject)
                    ?.let { runCatching { protonJson.decodeFromJsonElement<HumanVerificationChallenge>(it) }.getOrNull() }
                return Envelope(null, ProtonException.of(wire, challenge), sessionOf(error))
            }
            val result = root["result"] as? JsonObject
                ?: throw ProtonException.of(ProtonErrorCode.HELPER_FAILED.wire)
            return Envelope(result, null, sessionOf(result))
        }

        private fun sessionOf(container: JsonObject): ProtonSession? =
            (container["session"] as? JsonObject)?.let { ProtonSession(it.toString()) }
    }
}
