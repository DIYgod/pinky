package app.pinky.companion

import android.graphics.Rect
import android.os.Build
import android.os.Bundle
import android.view.accessibility.AccessibilityNodeInfo
import android.view.accessibility.AccessibilityWindowInfo
import org.json.JSONObject

/**
 * Writes text into an editable node with ACTION_SET_TEXT. The text goes
 * straight into the field, so any script works and the clipboard and the
 * keyboard stay untouched. Only call it on the accessibility worker thread.
 */
class TextSetter(private val service: PinkyAccessibilityService) {

    fun setText(req: JSONObject): String {
        if (!req.has("text")) throw RequestError("bad_request", "set_text needs text")
        val text = req.getString("text")
        val append = req.optBoolean("append")
        val submit = req.optBoolean("submit")
        val node = resolve(req.optJSONObject("target"))
        try {
            if (!node.isEditable) throw RequestError("not_editable", "the target node is not editable")
            if (!node.isEnabled) throw RequestError("disabled", "the target node is disabled")
            if (append && node.isPassword) {
                throw RequestError("unsupported", "cannot append to a password field: its text is unreadable")
            }
            val value = if (append) currentText(node) + text else text

            if (submit && !node.isFocused) node.performAction(AccessibilityNodeInfo.ACTION_FOCUS)
            val args = Bundle()
            args.putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, value)
            if (!node.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, args)) {
                throw RequestError("action_failed", "the app did not accept ACTION_SET_TEXT")
            }

            var submitted = false
            if (submit && Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                submitted = node.performAction(AccessibilityNodeInfo.AccessibilityAction.ACTION_IME_ENTER.id)
            }

            node.refresh()
            val j = Json()
            j.beginObject()
            j.field("window", node.windowId)
            j.field("node", node.hashCode())
            if (!node.isPassword) {
                val after = currentText(node)
                j.field("text", after)
                j.field("verified", after == value)
            }
            if (submit) j.field("submitted", submitted)
            j.endObject()
            return j.toString()
        } finally {
            TreeDumper.recycle(node)
        }
    }

    private fun currentText(n: AccessibilityNodeInfo): String =
        if (n.isShowingHintText) "" else n.text?.toString() ?: ""

    /**
     * Finds the node a request targets: the focused input, a node handle
     * from a tree response, the editable node under a point, or the first
     * editable node with a resource id. Windows are searched top-most first.
     */
    private fun resolve(t: JSONObject?): AccessibilityNodeInfo {
        if (t == null || t.length() == 0 || t.optBoolean("focused")) {
            return service.findFocus(AccessibilityNodeInfo.FOCUS_INPUT)
                ?: throw RequestError("not_found", "no input has focus")
        }
        val windows = service.windows.sortedByDescending { it.layer }
        try {
            if (t.has("node")) {
                val handle = t.getInt("node")
                val windowId = t.optInt("window", -1)
                for (w in windows) {
                    if (windowId != -1 && w.id != windowId) continue
                    find(w) { it.hashCode() == handle }?.let { return it }
                }
                throw RequestError("not_found", "node $handle is gone")
            }
            val id = t.optString("id", "")
            if (t.has("x") && t.has("y")) {
                val x = t.getInt("x")
                val y = t.getInt("y")
                for (w in windows) {
                    smallestEditableAt(w, x, y, id)?.let { return it }
                }
                throw RequestError("not_found", "no editable node at $x,$y")
            }
            if (id.isNotEmpty()) {
                for (w in windows) {
                    find(w) { it.isEditable && it.viewIdResourceName == id }?.let { return it }
                }
                throw RequestError("not_found", "no editable node with id $id")
            }
            throw RequestError("bad_request", "target needs focused, node, x and y, or id")
        } finally {
            TreeDumper.recycleWindows(windows)
        }
    }

    private fun find(w: AccessibilityWindowInfo, match: (AccessibilityNodeInfo) -> Boolean): AccessibilityNodeInfo? {
        val root = w.root ?: return null
        val stack = ArrayDeque<AccessibilityNodeInfo>()
        stack.addLast(root)
        var found: AccessibilityNodeInfo? = null
        while (stack.isNotEmpty()) {
            val n = stack.removeLast()
            if (found == null && n.isVisibleToUser && match(n)) {
                found = n
                continue
            }
            if (found == null) {
                for (i in n.childCount - 1 downTo 0) n.getChild(i)?.let { stack.addLast(it) }
            }
            TreeDumper.recycle(n)
        }
        return found
    }

    private fun smallestEditableAt(w: AccessibilityWindowInfo, x: Int, y: Int, id: String): AccessibilityNodeInfo? {
        val root = w.root ?: return null
        val r = Rect()
        val stack = ArrayDeque<AccessibilityNodeInfo>()
        stack.addLast(root)
        var best: AccessibilityNodeInfo? = null
        var bestScore = Long.MAX_VALUE
        while (stack.isNotEmpty()) {
            val n = stack.removeLast()
            var keep = false
            if (n.isVisibleToUser) {
                n.getBoundsInScreen(r)
                if (r.contains(x, y)) {
                    if (n.isEditable) {
                        // Prefer a matching resource id, then the smallest area.
                        val area = r.width().toLong() * r.height().toLong()
                        val score = if (id.isNotEmpty() && n.viewIdResourceName != id) area + (1L shl 40) else area
                        if (score < bestScore) {
                            best?.let { TreeDumper.recycle(it) }
                            best = n
                            bestScore = score
                            keep = true
                        }
                    }
                    for (i in 0 until n.childCount) n.getChild(i)?.let { stack.addLast(it) }
                }
            }
            if (!keep) TreeDumper.recycle(n)
        }
        return best
    }
}
