package org.tunaos.proton

import org.tunaos.proton.gomobile.protonsdk.Protonsdk

/**
 * Runs one request of the JSON contract and returns its reply envelope.
 * Blocking: [ProtonClient] calls it off the main thread.
 */
public fun interface ProtonTransport {
    public fun request(json: String): String
}

/** The Go library, bound by gomobile and linked into this AAR as libgojni.so. */
public object GoProtonTransport : ProtonTransport {
    override fun request(json: String): String = Protonsdk.request(json)
}
