package org.tunaos.proton

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.take
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.io.encoding.Base64
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlin.time.Duration

private fun session(token: String) =
    """{"login":"alice@proton.me","user_id":"u1","uid":"x1","access_token":"a-$token","refresh_token":"r-$token","password_mode":1,"key_pass":"a2V5"}"""

/** Replies from a script and records every request it was given. */
private class FakeTransport(vararg replies: String) : ProtonTransport {
    private val queue = ArrayDeque(replies.toList())
    val requests = mutableListOf<JsonObject>()

    override fun request(json: String): String {
        requests += protonJson.parseToJsonElement(json).jsonObject
        return queue.removeFirst()
    }
}

/** Remembers every saved session; can be told to fail. */
private class RecordingStore(private val failure: Exception? = null) : ProtonSessionStore {
    val saved = mutableListOf<ProtonSession>()

    override suspend fun save(session: ProtonSession) {
        saved += session
        failure?.let { throw it }
    }
}

private fun JsonObject.refreshToken(): String =
    getValue("session").jsonObject.getValue("refresh_token").jsonPrimitive.content

class ProtonAccountTest {
    private val header = """{"id":"m1","subject":"Hi","date_ms":1762500000000,"sender":{"name":"Bob","address":"bob@example.com"},
        "recipient":null,"is_unread":true,"confidential":true,"message_id":"abc@example.com","label_ids":["0","5"],
        "is_starred":false,"is_draft":false,"has_attachments":true,"size":2048}"""

    @Test
    fun syncParsesTheResultAndPersistsTheRotatedSessionFirst() = runTest {
        val transport = FakeTransport(
            """{"version":1,"result":{"folders":[{"id":"0","role":"inbox","label":"Inbox","parent":"","kind":"system","total":3,"unread":1},
               {"id":"f1","role":"other","label":"Receipts","parent":"","kind":"folder","total":0,"unread":0},
               {"id":"l1","role":"brand-new-role","label":"Work","parent":"","kind":"label","total":1,"unread":0}],
               "folder":"0","messages":[$header],"exists":3,"offset":0,"all_ids":["m1","m2","m3"],"next_cursor":null,
               "event_id":"ev1","session":${session("2")},"added_later":42}}""",
            """{"version":1,"result":{"event_id":"ev2","refresh":false,"more":false,"folders_changed":false,
               "mail_changed":[],"updated":[],"deleted":[],"session":${session("2")}}}""",
        )
        val store = RecordingStore()
        val account = ProtonClient(transport, StandardTestDispatcher(testScheduler)).account(ProtonSession(session("1")), store)

        val result = account.sync(limit = 50)

        assertEquals(listOf("r-2"), store.saved.map { protonJson.parseToJsonElement(it.json).jsonObject.getValue("refresh_token").jsonPrimitive.content })
        assertEquals(store.saved.last(), account.session)
        assertEquals(3, result.folders.size)
        assertEquals(ProtonFolderRole.INBOX, result.folders[0].role)
        assertEquals(ProtonFolderKind.FOLDER, result.folders[1].kind)
        assertEquals(ProtonFolderRole.OTHER, result.folders[2].role)
        assertEquals("ev1", result.eventId)
        assertEquals(listOf("m1", "m2", "m3"), result.allIds)
        assertNull(result.nextCursor)
        assertNull(result.changes)
        val message = result.messages.single()
        assertEquals(ProtonMailbox(address = "bob@example.com", name = "Bob"), message.sender)
        assertNull(message.recipient)
        assertTrue(message.isUnread && message.confidential && message.hasAttachments)
        assertEquals(listOf("0", "5"), message.labelIds)

        val request = transport.requests[0]
        assertEquals(1, request.getValue("version").jsonPrimitive.int)
        assertEquals("sync", request.getValue("op").jsonPrimitive.content)
        assertEquals(50, request.getValue("limit").jsonPrimitive.int)
        assertFalse("folder" in request)
        assertEquals("r-1", request.refreshToken())

        account.events("ev1")
        assertEquals("r-2", transport.requests[1].refreshToken(), "the next request must carry the rotated session")
    }

    @Test
    fun anErrorStillPersistsItsSessionBeforeThrowing() = runTest {
        val transport = FakeTransport(
            """{"version":1,"error":{"code":"transport_failed","session":${session("3")}}}""",
            """{"version":1,"result":{"outcomes":[{"id":"m1","code":null},{"id":"m2","code":"request_failed"},{"id":"m3","code":"not_attempted"}],"session":${session("3")}}}""",
        )
        val store = RecordingStore()
        val account = ProtonClient(transport, StandardTestDispatcher(testScheduler)).account(ProtonSession(session("1")), store)

        val error = assertFailsWith<ProtonException.Transport> { account.sync(limit = 1, since = "ev0") }
        assertEquals(ProtonErrorCode.TRANSPORT_FAILED, error.code)
        assertEquals(1, store.saved.size)

        val outcomes = account.markRead(listOf("m1", "m2", "m3"), isRead = true)
        assertEquals("r-3", transport.requests[1].refreshToken())
        assertTrue(transport.requests[1].getValue("is_read").jsonPrimitive.boolean)
        assertTrue(outcomes[0].isApplied)
        assertEquals(ProtonErrorCode.REQUEST_FAILED, outcomes[1].code)
        assertEquals(ProtonErrorCode.NOT_ATTEMPTED, outcomes[2].code)
    }

    @Test
    fun aStoreFailureDropsTheResultButKeepsTheSessionInMemory() = runTest {
        val transport = FakeTransport(
            """{"version":1,"result":{"outcomes":[],"session":${session("4")}}}""",
            """{"version":1,"result":{"outcomes":[],"session":${session("4")}}}""",
        )
        val store = RecordingStore(failure = IllegalStateException("disk full"))
        val account = ProtonClient(transport, StandardTestDispatcher(testScheduler)).account(ProtonSession(session("1")), store)

        assertFailsWith<IllegalStateException> { account.star(listOf("m1"), isStarred = true) }
        assertFailsWith<IllegalStateException> { account.move(listOf("m1"), destination = "6") }
        assertEquals("r-4", transport.requests[1].refreshToken())
    }

    @Test
    fun cancellationCannotLoseARotatedSession() = runBlocking {
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        val transport = ProtonTransport {
            entered.countDown()
            release.await(10, TimeUnit.SECONDS)
            """{"version":1,"result":{"outcomes":[],"session":${session("5")}}}"""
        }
        val store = RecordingStore()
        val account = ProtonClient(transport, Dispatchers.IO).account(ProtonSession(session("1")), store)

        val job = launch(Dispatchers.Default) { account.star(listOf("m1"), isStarred = false) }
        assertTrue(entered.await(10, TimeUnit.SECONDS))
        job.cancel()
        release.countDown()
        job.join()

        assertEquals(1, store.saved.size)
        assertEquals(store.saved.single(), account.session)
    }

    @Test
    fun loginSavesTheSessionAndHumanVerificationCarriesItsChallenge() = runTest {
        val transport = FakeTransport(
            """{"version":1,"error":{"code":"human_verification_required","hv":{"token":"tok en","methods":["captcha","email"]}}}""",
            """{"version":1,"error":{"code":"second_factor_required"}}""",
            """{"version":1,"result":{"session":${session("1")}}}""",
        )
        val store = RecordingStore()
        val client = ProtonClient(transport, StandardTestDispatcher(testScheduler))

        val hv = assertFailsWith<ProtonException.HumanVerificationRequired> { client.login("alice@proton.me", "pw", store) }
        val challenge = hv.challenge!!
        assertEquals(listOf("captcha", "email"), challenge.methods)
        assertEquals("https://verify.proton.me/?methods=captcha%2Cemail&token=tok+en&embed=true", challenge.verifyUrl())

        val factor = assertFailsWith<ProtonException.Auth> {
            client.login("alice@proton.me", "pw", store, humanVerification = HumanVerificationToken("solved", "captcha"))
        }
        assertEquals(ProtonErrorCode.SECOND_FACTOR_REQUIRED, factor.code)
        assertEquals("solved", transport.requests[1].getValue("hv_token").jsonPrimitive.content)
        assertEquals("captcha", transport.requests[1].getValue("hv_type").jsonPrimitive.content)
        assertFalse("totp" in transport.requests[1])
        assertTrue(store.saved.isEmpty())

        val account = client.login("alice@proton.me", "pw", store, totp = "123456")
        assertEquals("123456", transport.requests[2].getValue("totp").jsonPrimitive.content)
        assertEquals(listOf(account.session), store.saved)
        assertEquals("alice@proton.me", account.session.login)
        assertEquals("ProtonSession(<redacted>)", account.session.toString())
    }

    @Test
    fun messageDecodesTheMimeBytes() = runTest {
        val mime = "From: bob@example.com\r\nSubject: Hi\r\n\r\nGrüße\r\n".encodeToByteArray()
        val transport = FakeTransport(
            """{"version":1,"result":{"header":$header,"mime":"${Base64.encode(mime)}",
               "attachments":[{"id":"a1","name":"r.pdf","mime_type":"application/pdf","cid":null,"size":10,"disposition":"attachment"}],
               "session":${session("1")}}}""",
        )
        val account = ProtonClient(transport, StandardTestDispatcher(testScheduler)).account(ProtonSession(session("1")), RecordingStore())

        val message = account.message("m1")

        assertContentEquals(mime, message.mime)
        assertEquals("m1", message.header.id)
        assertEquals("application/pdf", message.attachments.single().mimeType)
        assertNull(message.attachments.single().cid)
    }

    @Test
    fun sendWritesTheContractShape() = runTest {
        val transport = FakeTransport(
            """{"version":1,"error":{"code":"send_uncertain","session":${session("2")}}}""",
            """{"version":1,"result":{"message_id":"id@proton.me","sent_mail":"s1","sent_date_ms":1,"internal_recipients":1,
               "external_recipients":0,"session":${session("2")}}}""",
        )
        val account = ProtonClient(transport, StandardTestDispatcher(testScheduler)).account(ProtonSession(session("1")), RecordingStore())
        val mail = ProtonOutgoingMail(
            sender = "alice@proton.me",
            subject = "Re: Hi",
            bodyHtml = "<p>Hello</p>",
            delivery = ProtonDelivery.SECURE,
            to = listOf(ProtonMailbox("bob@proton.me", "Bob")),
            attachments = listOf(ProtonOutgoingAttachment("a.txt", "text/plain", "hi".encodeToByteArray())),
            inReplyTo = "<abc@example.com>",
            action = ProtonReplyAction.REPLY_ALL,
        )

        assertFailsWith<ProtonException.SendUncertain> { account.send(mail) }
        val sent = account.send(mail.copy(delivery = ProtonDelivery.ORDINARY, ordinaryConfirmed = true, inReplyTo = null, action = null))

        val request = transport.requests[0]
        assertEquals("secure", request.getValue("delivery").jsonPrimitive.content)
        assertEquals("reply_all", request.getValue("action").jsonPrimitive.content)
        assertEquals("bob@proton.me", request.getValue("to").jsonArray.single().jsonObject.getValue("address").jsonPrimitive.content)
        assertEquals(0, request.getValue("cc").jsonArray.size)
        val attachment = request.getValue("attachments").jsonArray.single().jsonObject
        assertEquals("aGk=", attachment.getValue("data").jsonPrimitive.content)
        assertEquals(JsonNull, attachment.getValue("cid"))
        assertFalse("action" in transport.requests[1])
        assertFalse("in_reply_to" in transport.requests[1])
        assertTrue(transport.requests[1].getValue("ordinary_confirmed").jsonPrimitive.boolean)
        assertEquals("s1", sent.sentMail)
    }

    @Test
    fun codesMapOntoTheirCategories() {
        assertIs<ProtonException.Session>(ProtonException.of("session_revoked"))
        assertIs<ProtonException.Folder>(ProtonException.of("invalid_destination"))
        assertIs<ProtonException.Content>(ProtonException.of("decryption_failed"))
        assertIs<ProtonException.Send>(ProtonException.of("ordinary_delivery_requires_consent"))
        assertIs<ProtonException.InvalidRequest>(ProtonException.of("invalid_page"))
        val unknown = assertIs<ProtonException.Failed>(ProtonException.of("brand_new_code"))
        assertEquals(ProtonErrorCode.UNKNOWN, unknown.code)
        assertEquals("brand_new_code", unknown.wire)
        assertEquals(ProtonErrorCode.UNKNOWN, ProtonErrorCode.fromWire(""))
    }

    @Test
    fun malformedRepliesAreRefused() = runTest {
        val transport = FakeTransport("""{"version":2,"result":{}}""", "not json")
        val account = ProtonClient(transport, StandardTestDispatcher(testScheduler)).account(ProtonSession(session("1")), RecordingStore())

        assertEquals(ProtonErrorCode.UNSUPPORTED_VERSION, assertFailsWith<ProtonException.InvalidRequest> { account.events("e") }.code)
        assertEquals(ProtonErrorCode.HELPER_FAILED, assertFailsWith<ProtonException.Failed> { account.events("e") }.code)
    }

    @Test
    fun revokeEndsTheAccount() = runTest {
        val transport = FakeTransport("""{"version":1,"result":{}}""")
        val store = RecordingStore()
        val account = ProtonClient(transport, StandardTestDispatcher(testScheduler)).account(ProtonSession(session("1")), store)

        account.revoke()

        assertTrue(store.saved.isEmpty())
        assertEquals(ProtonErrorCode.SESSION_REVOKED, assertFailsWith<ProtonException.Session> { account.events("e") }.code)
        assertEquals(1, transport.requests.size)
    }

    @Test
    fun watchPollsAndReportsWhatChanged() = runTest {
        fun poll(id: String, body: String) =
            """{"version":1,"result":{"event_id":"$id",$body,"session":${session(id)}}}"""
        val transport = FakeTransport(
            poll("e1", """"refresh":false,"more":false,"folders_changed":false,"mail_changed":[],"updated":[],"deleted":[]"""),
            poll("e2", """"refresh":false,"more":true,"folders_changed":true,"mail_changed":["0","6"],"updated":["m1"],"deleted":["m2"]"""),
            poll("e3", """"refresh":true,"more":false,"folders_changed":true,"mail_changed":[],"updated":[],"deleted":[]"""),
        )
        val store = RecordingStore()
        val account = ProtonClient(transport, StandardTestDispatcher(testScheduler)).account(ProtonSession(session("1")), store)

        val events = account.watch(since = "e0", interval = Duration.ZERO).take(6).toList()

        assertEquals(
            listOf(
                ProtonWatchEvent.Ready("e1"),
                ProtonWatchEvent.FoldersChanged,
                ProtonWatchEvent.MailChanged("0"),
                ProtonWatchEvent.MailChanged("6"),
                ProtonWatchEvent.MessagesChanged(listOf("m1"), listOf("m2")),
                ProtonWatchEvent.Refresh,
            ),
            events,
        )
        assertEquals(listOf("e0", "e1", "e2"), transport.requests.map { it.getValue("since").jsonPrimitive.content })
        assertEquals(3, store.saved.size)
    }
}
