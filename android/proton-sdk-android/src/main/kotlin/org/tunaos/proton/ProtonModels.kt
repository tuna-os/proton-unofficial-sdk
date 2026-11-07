package org.tunaos.proton

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import java.net.URLEncoder

/** A `{name, address}` pair. */
@Serializable
public data class ProtonMailbox(
    public val address: String,
    public val name: String = "",
)

@Serializable
public enum class ProtonFolderRole {
    @SerialName("inbox") INBOX,
    @SerialName("drafts") DRAFTS,
    @SerialName("sent") SENT,
    @SerialName("starred") STARRED,
    @SerialName("archive") ARCHIVE,
    @SerialName("junk") JUNK,
    @SerialName("trash") TRASH,
    @SerialName("other") OTHER,
}

@Serializable
public enum class ProtonFolderKind {
    /** One of Proton's system labels shown as a folder. */
    @SerialName("system") SYSTEM,

    /** A user folder: exclusive, mail can be moved into it. */
    @SerialName("folder") FOLDER,

    /** A user label: a tag, not a place. */
    @SerialName("label") LABEL,
}

/** A folder row of `sync`. System folders have well-known ids (Inbox `0`, Sent `7`, ...). */
@Serializable
public data class ProtonFolder(
    public val id: String,
    public val role: ProtonFolderRole = ProtonFolderRole.OTHER,
    public val label: String,
    /** The parent folder's id, `""` at the top. */
    public val parent: String = "",
    public val kind: ProtonFolderKind = ProtonFolderKind.LABEL,
    public val total: Int = 0,
    public val unread: Int = 0,
)

/** A message's metadata, as `sync`, `events`-driven syncs and `message` return it. */
@Serializable
public data class ProtonMailHeader(
    public val id: String,
    public val subject: String,
    @SerialName("date_ms") public val dateMs: Long,
    public val sender: ProtonMailbox,
    /** The first To, else Cc, else Bcc recipient. */
    public val recipient: ProtonMailbox? = null,
    @SerialName("is_unread") public val isUnread: Boolean,
    /** Arrived end-to-end encrypted. */
    public val confidential: Boolean,
    /** The original Message-ID, without angle brackets. */
    @SerialName("message_id") public val messageId: String = "",
    @SerialName("label_ids") public val labelIds: List<String> = emptyList(),
    @SerialName("is_starred") public val isStarred: Boolean = false,
    @SerialName("is_draft") public val isDraft: Boolean = false,
    @SerialName("has_attachments") public val hasAttachments: Boolean = false,
    public val size: Long = 0,
)

@Serializable
public data class ProtonSyncChanges(
    /** Discard the deltas and sync from scratch from [ProtonSyncResult.eventId]. */
    public val refresh: Boolean,
    /** The event log goes on: sync again from [ProtonSyncResult.eventId]. */
    public val more: Boolean,
    public val deleted: List<String>,
    public val updated: List<ProtonMailHeader>,
)

@Serializable
public data class ProtonSyncResult(
    public val folders: List<ProtonFolder>,
    public val folder: String,
    /** Headers, newest first. */
    public val messages: List<ProtonMailHeader>,
    public val exists: Int,
    public val offset: Int,
    /** Every id in the folder, for a first page without cursor or since; else null. */
    @SerialName("all_ids") public val allIds: List<String>? = null,
    /** Pass as `cursor` for the next page; null at the end. */
    @SerialName("next_cursor") public val nextCursor: String? = null,
    /** Pass as `since` next time. */
    @SerialName("event_id") public val eventId: String,
    /** Only for a sync with `since`. */
    public val changes: ProtonSyncChanges? = null,
)

/**
 * What `search` looks for; at least one criterion besides [folder] is
 * required. Text criteria are case-insensitive substrings of the message's
 * metadata: bodies and attachments are end-to-end encrypted, so they cannot
 * be searched on the server. See the README's `search` section.
 */
public data class ProtonSearchQuery(
    /** Subject, sender or any recipient (name or address). */
    public val keyword: String? = null,
    public val subject: String? = null,
    /** The sender's name or address. */
    public val sender: String? = null,
    /** Any To, Cc or Bcc recipient's name or address. */
    public val recipient: String? = null,
    /** Sent at or after this instant, in epoch milliseconds. */
    public val afterMs: Long? = null,
    /** Sent before this instant, in epoch milliseconds. */
    public val beforeMs: Long? = null,
    public val isUnread: Boolean? = null,
    public val isStarred: Boolean? = null,
    /** A folder or label id as `sync` lists it; null searches All Mail (`5`). */
    public val folder: String? = null,
)

@Serializable
public data class ProtonSearchResult(
    /** Matching headers, newest first. */
    public val messages: List<ProtonMailHeader>,
    /** Pass as `cursor` to search on; null when the folder has been searched to the end. */
    @SerialName("next_cursor") public val nextCursor: String? = null,
)

/** The result of one `events` poll. */
@Serializable
public data class ProtonEvents(
    /** Poll from here next time. */
    @SerialName("event_id") public val eventId: String,
    /** The server demanded a full resync, or forgot the event id polled from. */
    public val refresh: Boolean,
    /** More events wait: poll again right away. */
    public val more: Boolean,
    /** A folder or label was created, renamed, moved or deleted. */
    @SerialName("folders_changed") public val foldersChanged: Boolean,
    /** Folders the created or changed mail is in now, sorted. */
    @SerialName("mail_changed") public val mailChanged: List<String>,
    /** Ids created or changed: also refresh whatever local folder holds them. */
    public val updated: List<String>,
    public val deleted: List<String>,
)

@Serializable
public data class ProtonAttachmentInfo(
    public val id: String,
    public val name: String,
    @SerialName("mime_type") public val mimeType: String,
    public val cid: String? = null,
    public val size: Long = 0,
    public val disposition: String = "",
)

/** A decrypted message: the finished RFC 5322 bytes, attachments inside. */
public class ProtonMessage(
    public val header: ProtonMailHeader,
    public val mime: ByteArray,
    /** Metadata only; the bytes are inside [mime]. */
    public val attachments: List<ProtonAttachmentInfo>,
)

/** One id's outcome of `mark_read`, `move` or `star`: [code] is null when applied. */
public data class ProtonOutcome(
    public val id: String,
    public val wire: String?,
) {
    public val isApplied: Boolean get() = wire == null
    public val code: ProtonErrorCode? get() = wire?.let(ProtonErrorCode::fromWire)
}

public enum class ProtonDelivery(internal val wire: String) {
    /** End-to-end encrypted to every recipient, or refused. */
    SECURE("secure"),

    /** Whatever each recipient supports; needs the user's consent. */
    ORDINARY("ordinary"),
}

public enum class ProtonReplyAction(internal val wire: String) {
    REPLY("reply"),
    REPLY_ALL("reply_all"),
    FORWARD("forward"),
}

public class ProtonOutgoingAttachment(
    public val name: String,
    public val mimeType: String,
    public val data: ByteArray,
    /** A Content-ID makes the attachment inline. */
    public val cid: String? = null,
)

/** A mail to send; see the README's `send` section for each field's rules. */
public data class ProtonOutgoingMail(
    public val sender: String,
    public val subject: String,
    public val bodyHtml: String,
    public val delivery: ProtonDelivery,
    public val senderName: String = "",
    public val to: List<ProtonMailbox> = emptyList(),
    public val cc: List<ProtonMailbox> = emptyList(),
    public val bcc: List<ProtonMailbox> = emptyList(),
    public val replyTos: List<ProtonMailbox> = emptyList(),
    public val ordinaryConfirmed: Boolean = false,
    public val attachments: List<ProtonOutgoingAttachment> = emptyList(),
    public val messageId: String? = null,
    public val inReplyTo: String? = null,
    public val references: String? = null,
    /** Only with [inReplyTo] or [references]. */
    public val action: ProtonReplyAction? = null,
)

@Serializable
public data class ProtonSendResult(
    /** The sent mail's Message-ID, without angle brackets. */
    @SerialName("message_id") public val messageId: String,
    /** The Proton id of the Sent copy. */
    @SerialName("sent_mail") public val sentMail: String,
    @SerialName("sent_date_ms") public val sentDateMs: Long,
    @SerialName("internal_recipients") public val internalRecipients: Int,
    @SerialName("external_recipients") public val externalRecipients: Int,
)

/** A draft to save; the fields follow [ProtonOutgoingMail], and recipients may be empty. */
public data class ProtonDraft(
    public val sender: String,
    public val subject: String = "",
    public val bodyHtml: String = "",
    public val senderName: String = "",
    public val to: List<ProtonMailbox> = emptyList(),
    public val cc: List<ProtonMailbox> = emptyList(),
    public val bcc: List<ProtonMailbox> = emptyList(),
    public val replyTos: List<ProtonMailbox> = emptyList(),
    public val attachments: List<ProtonOutgoingAttachment> = emptyList(),
    public val messageId: String? = null,
    public val inReplyTo: String? = null,
    public val references: String? = null,
    /** Only with [inReplyTo] or [references]. */
    public val action: ProtonReplyAction? = null,
)

@Serializable
public data class ProtonDraftResult(
    /** The new draft's Proton id. */
    public val id: String,
    /** Its Message-ID, without angle brackets. */
    @SerialName("message_id") public val messageId: String,
    /** Whether the draft it replaces was deleted; false leaves a duplicate behind. */
    public val replaced: Boolean,
)

@Serializable
public data class ProtonEmptyResult(
    /** How many messages this run deleted. */
    public val deleted: Int,
    /** The run stopped for its time budget: call again. */
    public val more: Boolean,
)

/** A human-verification challenge from a refused login. */
@Serializable
public data class HumanVerificationChallenge(
    public val token: String,
    public val methods: List<String>,
) {
    /**
     * The page to open in a WebView. The solved token arrives as a
     * `HUMAN_VERIFICATION_SUCCESS` message posted by the page; see the
     * README's human verification section.
     */
    public fun verifyUrl(): String {
        val methods = URLEncoder.encode(methods.joinToString(","), "UTF-8")
        val token = URLEncoder.encode(token, "UTF-8")
        return "https://verify.proton.me/?methods=$methods&token=$token&embed=true"
    }
}

/** A solved challenge to retry the login with: `payload.token` and `payload.type`. */
public data class HumanVerificationToken(
    public val token: String,
    public val type: String,
)

/** What [ProtonAccount.watch] reports. */
public sealed interface ProtonWatchEvent {
    /** The first poll succeeded; sync everything once. */
    public data class Ready(public val eventId: String) : ProtonWatchEvent

    /** The server wants a full resync. */
    public data object Refresh : ProtonWatchEvent

    /** Folders or labels changed: re-read the folder list. */
    public data object FoldersChanged : ProtonWatchEvent

    /** Mail in [folder] arrived, left or changed. */
    public data class MailChanged(public val folder: String) : ProtonWatchEvent

    /** These message ids changed or went away; refresh the folders that hold them locally. */
    public data class MessagesChanged(
        public val updated: List<String>,
        public val deleted: List<String>,
    ) : ProtonWatchEvent
}
