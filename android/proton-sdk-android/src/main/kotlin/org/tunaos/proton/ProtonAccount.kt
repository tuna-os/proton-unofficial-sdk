package org.tunaos.proton

import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonObjectBuilder
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.decodeFromJsonElement
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import kotlin.io.encoding.Base64
import kotlin.time.Duration
import kotlin.time.Duration.Companion.seconds

/**
 * One signed-in Proton account. It owns the session: requests run one at a
 * time, each with the session the previous one left behind, and every
 * session a reply carries is saved to the store before the reply's result
 * is returned or its error thrown. Two requests in flight with the same
 * session would race on its refresh token, so keep exactly one
 * [ProtonAccount] per account and session.
 */
public class ProtonAccount internal constructor(
    session: ProtonSession,
    private val store: ProtonSessionStore,
    private val client: ProtonClient,
) {
    private val mutex = Mutex()
    @Volatile private var current: ProtonSession = session

    @Volatile private var isRevoked = false

    /** The session as it stands now, already saved to the store. */
    public val session: ProtonSession get() = current

    /**
     * Lists [folder] (default Inbox `0`) and every folder row. Page with
     * [cursor] (a message id) rather than [offset]; pass a previous
     * [ProtonSyncResult.eventId] as [since] to get [ProtonSyncResult.changes].
     */
    public suspend fun sync(
        limit: Int,
        folder: String? = null,
        offset: Int? = null,
        cursor: String? = null,
        since: String? = null,
    ): ProtonSyncResult = decode(
        call("sync") {
            put("limit", limit)
            folder?.let { put("folder", it) }
            offset?.let { put("offset", it) }
            cursor?.let { put("cursor", it) }
            since?.let { put("since", it) }
        },
    )

    /** What changed since [since] (an event id from `sync` or a previous poll). */
    public suspend fun events(since: String): ProtonEvents = decode(call("events") { put("since", since) })

    /** Fetches and decrypts one message into RFC 5322 bytes. */
    public suspend fun message(id: String): ProtonMessage {
        val result = call("message") { put("id", id) }
        return ProtonMessage(
            header = protonJson.decodeFromJsonElement(result.getValue("header")),
            mime = Base64.decode(result.getValue("mime").jsonPrimitive.content),
            attachments = protonJson.decodeFromJsonElement(result.getValue("attachments")),
        )
    }

    /** Sets read state on at most 50 ids; one outcome per id, in order. */
    public suspend fun markRead(ids: List<String>, isRead: Boolean): List<ProtonOutcome> =
        outcomes(call("mark_read") { put("ids", strings(ids)); put("is_read", isRead) })

    /** Stars or unstars at most 50 ids. */
    public suspend fun star(ids: List<String>, isStarred: Boolean): List<ProtonOutcome> =
        outcomes(call("star") { put("ids", strings(ids)); put("is_starred", isStarred) })

    /** Moves at most 50 ids to Inbox, Archive, Spam, Trash or a user folder. */
    public suspend fun move(ids: List<String>, destination: String): List<ProtonOutcome> =
        outcomes(call("move") { put("ids", strings(ids)); put("destination", destination) })

    /**
     * Encrypts and sends [mail].
     *
     * @throws ProtonException.SendUncertain when the submit may have
     *   delivered; never retry that automatically.
     */
    public suspend fun send(mail: ProtonOutgoingMail): ProtonSendResult = decode(
        call("send") {
            put("sender", mail.sender)
            put("sender_name", mail.senderName)
            put("to", mailboxes(mail.to))
            put("cc", mailboxes(mail.cc))
            put("bcc", mailboxes(mail.bcc))
            put("reply_tos", mailboxes(mail.replyTos))
            put("subject", mail.subject)
            put("body_html", mail.bodyHtml)
            put("delivery", mail.delivery.wire)
            put("ordinary_confirmed", mail.ordinaryConfirmed)
            put(
                "attachments",
                buildJsonArray {
                    for (attachment in mail.attachments) {
                        add(
                            buildJsonObject {
                                put("name", attachment.name)
                                put("mime_type", attachment.mimeType)
                                put("cid", attachment.cid?.let { JsonPrimitive(it) } ?: JsonNull)
                                put("data", Base64.encode(attachment.data))
                            },
                        )
                    }
                },
            )
            mail.messageId?.let { put("message_id", it) }
            mail.inReplyTo?.let { put("in_reply_to", it) }
            mail.references?.let { put("references", it) }
            mail.action?.let { put("action", it.wire) }
        },
    )

    /**
     * Logs the session out server-side. Idempotent; afterwards this account
     * refuses every request with [ProtonErrorCode.SESSION_REVOKED].
     */
    public suspend fun revoke() {
        call("revoke") {}
        isRevoked = true
    }

    /**
     * Any operation of the JSON contract by name, with this account's
     * session filled in and its rotation handled; [params] are the
     * operation's own fields. Returns the reply's `result`.
     */
    public suspend fun rawRequest(op: String, params: JsonObject = JsonObject(emptyMap())): JsonObject =
        call(op) { params.forEach { (key, value) -> put(key, value) } }

    /**
     * Watches the account by polling [events]: Proton has no push channel
     * for third-party clients. Starts from [since] (the event id the caller
     * last synced from), emits [ProtonWatchEvent.Ready] after the first
     * poll, then whatever each poll found, waiting [interval] between polls
     * that left nothing pending. Ends with the [ProtonException] of a poll
     * that fails; wrap it in `retryWhen` to ride out [ProtonException.Transport].
     * Polls share this account's queue, so they never race other requests.
     */
    public fun watch(since: String, interval: Duration = 30.seconds): Flow<ProtonWatchEvent> = flow {
        var eventId = since
        var isFirst = true
        while (true) {
            val polled = events(eventId)
            eventId = polled.eventId
            if (isFirst) {
                emit(ProtonWatchEvent.Ready(eventId))
                isFirst = false
            }
            if (polled.refresh) {
                emit(ProtonWatchEvent.Refresh)
            } else {
                if (polled.foldersChanged) emit(ProtonWatchEvent.FoldersChanged)
                polled.mailChanged.forEach { emit(ProtonWatchEvent.MailChanged(it)) }
                if (polled.updated.isNotEmpty() || polled.deleted.isNotEmpty()) {
                    emit(ProtonWatchEvent.MessagesChanged(polled.updated, polled.deleted))
                }
            }
            if (!polled.more) delay(interval)
        }
    }

    private suspend fun call(op: String, fields: JsonObjectBuilder.() -> Unit): JsonObject = mutex.withLock {
        if (isRevoked) throw ProtonException.of(ProtonErrorCode.SESSION_REVOKED.wire)
        val request = buildJsonObject {
            fields()
            put("version", PROTOCOL_VERSION)
            put("op", op)
            put("session", current.toJson())
        }
        client.exchange(request, store) { current = it }.resultOrThrow()
    }

    private inline fun <reified T> decode(result: JsonObject): T = protonJson.decodeFromJsonElement(result)

    private fun outcomes(result: JsonObject): List<ProtonOutcome> =
        result.getValue("outcomes").jsonArray.map { row ->
            val fields = row.jsonObject
            val code = fields["code"]
            ProtonOutcome(
                id = fields.getValue("id").jsonPrimitive.content,
                wire = if (code == null || code is JsonNull) null else code.jsonPrimitive.content,
            )
        }

    private fun strings(values: List<String>): JsonArray = JsonArray(values.map { JsonPrimitive(it) })

    private fun mailboxes(values: List<ProtonMailbox>): JsonElement =
        protonJson.encodeToJsonElement(ListSerializer(ProtonMailbox.serializer()), values)
}
