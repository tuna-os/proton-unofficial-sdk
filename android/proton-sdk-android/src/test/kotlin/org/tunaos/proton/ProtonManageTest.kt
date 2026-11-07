package org.tunaos.proton

import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class ProtonManageTest {
    private fun ok(body: String) = """{"version":1,"result":{$body,"session":${session("2")}}}"""

    private fun TestScope.account(transport: FakeTransport, store: RecordingStore = RecordingStore()) =
        ProtonClient(transport, StandardTestDispatcher(testScheduler)).account(ProtonSession(session("1")), store)

    private val folderRow = """"folder":{"id":"f9","role":"other","label":"Receipts","parent":"f1","kind":"folder","total":0,"unread":0}"""

    @Test
    fun deleteLabelAndEmptyWriteTheContractShape() = runTest {
        val transport = FakeTransport(
            ok(""""outcomes":[{"id":"m1","code":null},{"id":"m2","code":"request_failed"}]"""),
            ok(""""outcomes":[{"id":"m1","code":null}]"""),
            ok(""""deleted":150,"more":true"""),
        )
        val store = RecordingStore()
        val account = account(transport, store)

        val deleted = account.delete(listOf("m1", "m2"))
        val labelled = account.label(listOf("m1"), label = "l1", isLabelled = false)
        val emptied = account.emptyFolder("3")

        assertEquals(listOf(true, false), deleted.map { it.isApplied })
        assertEquals(ProtonErrorCode.REQUEST_FAILED, deleted[1].code)
        assertTrue(labelled.single().isApplied)
        assertEquals(ProtonEmptyResult(deleted = 150, more = true), emptied)
        assertEquals(3, store.saved.size)

        val (delete, label, empty) = transport.requests
        assertEquals("delete", delete.getValue("op").jsonPrimitive.content)
        assertEquals(listOf("m1", "m2"), delete.getValue("ids").jsonArray.map { it.jsonPrimitive.content })
        assertEquals("label", label.getValue("op").jsonPrimitive.content)
        assertEquals("l1", label.getValue("label").jsonPrimitive.content)
        assertFalse(label.getValue("is_labelled").jsonPrimitive.boolean)
        assertEquals("empty_folder", empty.getValue("op").jsonPrimitive.content)
        assertEquals("3", empty.getValue("folder").jsonPrimitive.content)
        assertEquals("r-2", empty.refreshToken())
    }

    @Test
    fun saveDraftSendsTheDraftFieldsOnly() = runTest {
        val transport = FakeTransport(
            ok(""""id":"d2","message_id":"abc@proton.me","replaced":true"""),
            """{"version":1,"error":{"code":"sender_unknown","session":${session("3")}}}""",
        )
        val store = RecordingStore()
        val account = account(transport, store)
        val draft = ProtonDraft(
            sender = "alice@proton.me",
            subject = "Later",
            bodyHtml = "<p>half</p>",
            cc = listOf(ProtonMailbox("bob@proton.me")),
            attachments = listOf(ProtonOutgoingAttachment("n.txt", "text/plain", "hi".encodeToByteArray(), cid = "c1")),
            inReplyTo = "<p@example.com>",
            action = ProtonReplyAction.FORWARD,
        )

        val saved = account.saveDraft(draft, replaces = "d1")

        assertEquals(ProtonDraftResult(id = "d2", messageId = "abc@proton.me", replaced = true), saved)
        val request = transport.requests[0]
        assertEquals("save_draft", request.getValue("op").jsonPrimitive.content)
        assertEquals("d1", request.getValue("replaces").jsonPrimitive.content)
        assertEquals("forward", request.getValue("action").jsonPrimitive.content)
        assertEquals("c1", request.getValue("attachments").jsonArray.single().jsonObject.getValue("cid").jsonPrimitive.content)
        assertEquals(0, request.getValue("to").jsonArray.size)
        assertFalse("delivery" in request || "ordinary_confirmed" in request, "a draft carries no delivery fields")

        val refused = assertFailsWith<ProtonException.Send> { account.saveDraft(ProtonDraft(sender = "mallory@example.com")) }
        assertEquals(ProtonErrorCode.SENDER_UNKNOWN, refused.code)
        assertFalse("replaces" in transport.requests[1])
        assertEquals(2, store.saved.size)
    }

    @Test
    fun foldersAreCreatedRenamedAndDeleted() = runTest {
        val transport = FakeTransport(
            ok(folderRow),
            ok(folderRow.replace("Receipts", "Bills")),
            """{"version":1,"result":{"session":${session("2")}}}""",
            """{"version":1,"error":{"code":"folder_readonly","session":${session("2")}}}""",
        )
        val account = account(transport)

        val created = account.createFolder("Receipts", ProtonFolderKind.FOLDER, parent = "f1", color = "#7272a7")
        val renamed = account.renameFolder("f9", "Bills")
        account.deleteFolder("f9")
        val readonly = assertFailsWith<ProtonException.Folder> { account.deleteFolder("0") }

        assertEquals(ProtonFolder("f9", ProtonFolderRole.OTHER, "Receipts", "f1", ProtonFolderKind.FOLDER), created)
        assertEquals("Bills", renamed.label)
        assertEquals(ProtonErrorCode.FOLDER_READONLY, readonly.code)
        val (create, rename, delete) = transport.requests
        assertEquals("folder", create.getValue("kind").jsonPrimitive.content)
        assertEquals("f1", create.getValue("parent").jsonPrimitive.content)
        assertEquals("#7272a7", create.getValue("color").jsonPrimitive.content)
        assertEquals("Bills", rename.getValue("name").jsonPrimitive.content)
        assertEquals("delete_folder", delete.getValue("op").jsonPrimitive.content)

        val label = FakeTransport(ok(folderRow.replace("\"folder\",", "\"label\",")))
        assertEquals(ProtonFolderKind.LABEL, account(label).createFolder("Work", ProtonFolderKind.LABEL).kind)
        assertEquals("label", label.requests.single().getValue("kind").jsonPrimitive.content)
        assertNull(label.requests.single()["parent"])

        assertFailsWith<IllegalArgumentException> { account(FakeTransport()).createFolder("x", ProtonFolderKind.SYSTEM) }
    }

    /** The wrapper's codes are exactly the README's Error codes table, plus the outcome-only `not_attempted`. */
    @Test
    fun errorCodesMatchTheContract() {
        val readme = File("../../README.md").readText()
        val table = readme.substringAfter("\n## Error codes\n").substringBefore("\n## ")
        val documented = Regex("(?m)^\\| `([a-z_]+)` \\|").findAll(table).map { it.groupValues[1] }.toSet()
        assertTrue(documented.size > 30, "README table parsed as $documented")

        val wrapped = ProtonErrorCode.entries
            .filter { it != ProtonErrorCode.UNKNOWN && it != ProtonErrorCode.NOT_ATTEMPTED }
            .map { it.wire }
            .toSet()
        assertEquals(documented, wrapped)
        for (wire in documented) {
            assertEquals(wire, ProtonException.of(wire).wire)
            assertTrue(ProtonException.of(wire) !is ProtonException.Failed || wire in setOf("helper_failed", "request_failed"), wire)
        }
    }
}
