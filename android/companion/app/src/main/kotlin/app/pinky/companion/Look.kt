package app.pinky.companion

import android.accessibilityservice.AccessibilityServiceInfo
import android.app.Activity
import android.app.AlertDialog
import android.content.ActivityNotFoundException
import android.content.Intent
import android.content.res.ColorStateList
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.ColorDrawable
import android.graphics.drawable.Drawable
import android.graphics.drawable.GradientDrawable
import android.graphics.drawable.RippleDrawable
import android.net.Uri
import android.os.Build
import android.provider.Settings
import android.text.Editable
import android.text.InputType
import android.text.TextUtils
import android.text.TextWatcher
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.WindowInsets
import android.view.WindowManager
import android.view.accessibility.AccessibilityManager
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.RadioButton
import android.widget.ScrollView
import android.widget.TextView

/**
 * The look of Pinky's own screens on the phone (TaskActivity,
 * SettingsActivity, HistoryActivity and PastTaskActivity), built in code:
 * desktop Pinky's palette
 * (res/values/colors.xml, dark in values-night) and type, grouped rows,
 * buttons and dialogs.
 *
 * Each screen's content and each dialog's content is
 * accessibilityDataSensitive and drops touches that come through another
 * app's window (sensitive), so the agent can neither read nor press them.
 * Only each window's root stays visible to Pinky's service, which must see
 * that Pinky is in front to keep agents off it (policy.IsPinkyScreen,
 * OwnUI).
 */
class Look(private val activity: Activity) {

    private val density = activity.resources.displayMetrics.density

    val bg = activity.getColor(R.color.pinky_bg)
    val groupBg = activity.getColor(R.color.pinky_group)
    val line = activity.getColor(R.color.pinky_line)
    val bg3 = activity.getColor(R.color.pinky_bg3)
    val ink = activity.getColor(R.color.pinky_ink)
    val ink2 = activity.getColor(R.color.pinky_ink2)
    val ink3 = activity.getColor(R.color.pinky_ink3)
    val accent = activity.getColor(R.color.pinky_accent)
    val accentButton = activity.getColor(R.color.pinky_accent_button)
    val accentTint = activity.getColor(R.color.pinky_accent_tint)
    val red = activity.getColor(R.color.pinky_red)
    val redTint = activity.getColor(R.color.pinky_red_tint)
    val green = activity.getColor(R.color.pinky_green)
    val amber = activity.getColor(R.color.pinky_amber)
    val brand = activity.getColor(R.color.pinky_brand)
    private val ripple = (ink and 0x00ffffff) or 0x24000000

    private var dialog: AlertDialog? = null
    private var dialogInput: EditText? = null // the dialog's box, unless it holds a secret

    fun dp(v: Int): Int = (v * density + 0.5f).toInt()

    // The screen.

    /** The screen's column, which the caller fills before setScreen. */
    fun column(): LinearLayout = LinearLayout(activity).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(dp(16), dp(8), dp(16), dp(32))
    }

    /**
     * Shows the column as the activity's content: scrolling, sensitive,
     * and clear of the bars and the keyboard. From Android 11 the window
     * is edge to edge and the column keeps clear of them itself; before,
     * the window does it (adjustResize).
     */
    fun setScreen(column: LinearLayout) {
        sensitive(column)
        val scroll = ScrollView(activity).apply {
            setBackgroundColor(bg)
            isFillViewport = true
            addView(column, ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            activity.window.setDecorFitsSystemWindows(false)
            scroll.setOnApplyWindowInsetsListener { v, insets ->
                val i = insets.getInsets(
                    WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout() or WindowInsets.Type.ime(),
                )
                v.setPadding(i.left, i.top, i.right, i.bottom)
                WindowInsets.CONSUMED
            }
        }
        activity.setContentView(scroll)
    }

    /** The screen's title; the caller may add a button after it. */
    fun header(title: String, color: Int = ink): LinearLayout = LinearLayout(activity).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
        setPadding(dp(4), dp(12), 0, dp(4))
        addView(text(title, TITLE1, color, 700), LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
    }

    /** A section's heading above a group. */
    fun section(title: String): TextView = text(title, SMALL, ink3, 600).apply {
        setPadding(dp(4), dp(22), dp(4), dp(8))
    }

    /** A small note below a group. */
    fun note(s: String = ""): TextView = text(s, SMALL, ink3).apply {
        setPadding(dp(4), dp(8), dp(4), 0)
    }

    // Type.

    fun text(s: CharSequence = "", size: Float = BODY, color: Int = ink, weight: Int = 400): TextView =
        TextView(activity).apply {
            text = s
            setTextSize(TypedValue.COMPLEX_UNIT_SP, size)
            setTextColor(color)
            if (weight != 400) typeface = Typeface.create(Typeface.DEFAULT, weight, false)
            setLineSpacing(0f, 1.12f)
        }

    // Groups of rows.

    /** A rounded group; add rows with addRow. */
    fun group(): LinearLayout = LinearLayout(activity).apply {
        orientation = LinearLayout.VERTICAL
        background = rounded(groupBg, 14)
        clipToOutline = true
    }

    /** Adds a row to a group, below a hairline when it is not the first. */
    fun addRow(group: LinearLayout, row: View) {
        if (group.childCount > 0) {
            group.addView(
                View(activity).apply { setBackgroundColor(line) },
                LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 1).apply { marginStart = dp(16) },
            )
        }
        group.addView(row, matchWrap())
    }

    /** A row: its title, a value on the right, a description below. */
    class Row(val view: View, val title: TextView, val value: TextView, val desc: TextView)

    fun row(title: String, value: String = "", desc: String = "", onClick: (() -> Unit)? = null): Row {
        val t = text(title, BODY, ink)
        val v = text(value, BODY, ink3).apply {
            maxLines = 1
            ellipsize = TextUtils.TruncateAt.END
            gravity = Gravity.END
            maxWidth = (activity.resources.displayMetrics.widthPixels * 0.5).toInt()
        }
        v.say(value)
        val top = LinearLayout(activity).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            addView(t, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
            addView(
                v,
                LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT)
                    .apply { marginStart = dp(12) },
            )
        }
        val d = text(desc, SMALL, ink3).apply { setPadding(0, dp(3), 0, 0) }
        d.say(desc)
        val box = LinearLayout(activity).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER_VERTICAL
            minimumHeight = dp(52)
            setPadding(dp(16), dp(12), dp(16), dp(12))
            addView(top, matchWrap())
            addView(d, matchWrap())
        }
        if (onClick != null) clickable(box, onClick)
        return Row(box, t, v, d)
    }

    /** A paragraph inside a group. */
    fun paragraph(s: String): TextView = text(s, SMALL, ink2).apply {
        setPadding(dp(16), dp(12), dp(16), dp(12))
    }

    /** One of a group's choices: a title, a description and a radio mark. */
    class Choice(val view: View, val radio: RadioButton)

    fun choice(title: String, desc: String, onClick: () -> Unit): Choice {
        val texts = LinearLayout(activity).apply {
            orientation = LinearLayout.VERTICAL
            addView(text(title, BODY, ink))
            addView(text(desc, SMALL, ink3).apply { setPadding(0, dp(3), 0, 0) })
        }
        val radio = RadioButton(activity).apply {
            isClickable = false
            isFocusable = false
            importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
            buttonTintList = ColorStateList(
                arrayOf(intArrayOf(android.R.attr.state_checked), intArrayOf()),
                intArrayOf(accent, ink3),
            )
        }
        val box = LinearLayout(activity).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            minimumHeight = dp(56)
            setPadding(dp(16), dp(12), dp(8), dp(12))
            addView(texts, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
            addView(radio)
        }
        clickable(box, onClick)
        return Choice(box, radio)
    }

    /** A card for a message, with buttons added below it. */
    fun card(fill: Int = groupBg): LinearLayout = LinearLayout(activity).apply {
        orientation = LinearLayout.VERTICAL
        background = rounded(fill, 14)
        setPadding(dp(16), dp(14), dp(16), dp(14))
    }

    // Buttons.

    enum class Kind { PRIMARY, SECONDARY, DANGER, PLAIN }

    fun button(label: String, kind: Kind = Kind.SECONDARY, onClick: () -> Unit): TextView =
        text(label, BODY, ink, 600).apply {
            gravity = Gravity.CENTER
            minHeight = dp(44)
            minWidth = dp(88)
            setPadding(dp(18), dp(10), dp(18), dp(10))
            val fill = when (kind) {
                Kind.PRIMARY -> accentButton
                Kind.SECONDARY, Kind.DANGER -> bg3
                Kind.PLAIN -> Color.TRANSPARENT
            }
            setTextColor(
                when (kind) {
                    Kind.PRIMARY -> Color.WHITE
                    Kind.SECONDARY -> ink
                    Kind.DANGER -> red
                    Kind.PLAIN -> accent
                },
            )
            background = RippleDrawable(ColorStateList.valueOf(ripple), rounded(fill, 12), rounded(Color.WHITE, 12))
            setOnClickListener { onClick() }
        }

    /** A row of buttons at the end of a card. */
    fun buttons(vararg b: View): LinearLayout = LinearLayout(activity).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.END
        setPadding(0, dp(12), 0, 0)
        b.forEachIndexed { i, v ->
            addView(
                v,
                LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT)
                    .apply { if (i > 0) marginStart = dp(8) },
            )
        }
    }

    fun enable(v: View, on: Boolean) {
        v.isEnabled = on
        v.alpha = if (on) 1f else 0.45f
    }

    // Dialogs: one at a time, dismissed with the screen, which may open it
    // again when it is made anew (dialogOpen, dialogText).

    /** Picks one of options; checked is the current one, or -1. */
    fun choose(title: String, options: List<String>, checked: Int, onPick: (Int) -> Unit) {
        show(
            AlertDialog.Builder(activity)
                .setTitle(title)
                .setSingleChoiceItems(options.toTypedArray(), checked) { d, which ->
                    d.dismiss()
                    onPick(which)
                }
                .setNegativeButton(R.string.phone_cancel, null)
                .create(),
        )
    }

    /** A menu of actions. */
    fun menu(title: String, items: List<String>, onPick: (Int) -> Unit) {
        show(
            AlertDialog.Builder(activity)
                .setTitle(title)
                .setItems(items.toTypedArray()) { _, which -> onPick(which) }
                .setNegativeButton(R.string.phone_cancel, null)
                .create(),
        )
    }

    enum class Field { TEXT, URL, SECRET }

    /**
     * Asks for a value. check returns why the value cannot be saved, or
     * null; a secret is never saved with the dialog's state, autofilled or
     * suggested, and Save waits for one.
     */
    fun edit(
        title: String,
        desc: String,
        initial: String,
        hint: String,
        field: Field,
        check: (String) -> String? = { null },
        onSave: (String) -> Unit,
    ) {
        val input = EditText(activity).apply {
            setText(initial)
            setSelection(initial.length)
            this.hint = hint
            isSingleLine = true
            inputType = when (field) {
                Field.TEXT -> InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
                Field.URL -> InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI
                Field.SECRET -> InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD or
                    InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
            }
            if (field == Field.SECRET) {
                typeface = Typeface.DEFAULT
                isSaveEnabled = false
                importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS
            }
        }
        val error = text("", SMALL, red).apply { setPadding(dp(4), dp(4), dp(4), 0) }
        error.say("")
        val box = LinearLayout(activity).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(20), dp(8), dp(20), 0)
            addView(input, matchWrap())
            addView(error, matchWrap())
            if (desc.isNotEmpty()) addView(text(desc, SMALL, ink3).apply { setPadding(dp(4), dp(8), dp(4), 0) }, matchWrap())
        }
        val d = AlertDialog.Builder(activity)
            .setTitle(title)
            .setView(box)
            .setPositiveButton(R.string.phone_save, null)
            .setNegativeButton(R.string.phone_cancel, null)
            .create()
        d.setOnShowListener {
            val save = d.getButton(AlertDialog.BUTTON_POSITIVE)
            if (field == Field.SECRET) save.isEnabled = input.text.isNotBlank()
            input.addTextChangedListener(object : TextWatcher {
                override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) {}
                override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) {}
                override fun afterTextChanged(s: Editable?) {
                    // Why the value could not be saved holds until it changes.
                    error.say("")
                    if (field == Field.SECRET) save.isEnabled = !s.isNullOrBlank()
                }
            })
            save.setOnClickListener {
                val v = input.text.toString()
                val why = check(v)
                if (why != null) {
                    error.say(why)
                } else {
                    d.dismiss()
                    onSave(v)
                }
            }
        }
        d.window?.setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_STATE_VISIBLE)
        show(d, input.takeIf { field != Field.SECRET })
        input.requestFocus()
    }

    private fun show(d: AlertDialog, input: EditText? = null) {
        dialog?.dismiss()
        dialog = d
        dialogInput = input
        d.show()
        d.findViewById<View>(android.R.id.content)?.let { sensitive(it) }
    }

    fun dismissDialog() {
        dialog?.dismiss()
        dialog = null
        dialogInput = null
    }

    /** Whether a dialog is open: one the person has not answered or closed yet. */
    fun dialogOpen(): Boolean = dialog?.isShowing == true

    /**
     * What the open dialog's box holds, for the screen to open it again
     * with when it is made anew; null without a box, and for a secret.
     */
    fun dialogText(): String? = if (dialogOpen()) dialogInput?.text?.toString() else null

    // Android's settings.

    /** Whether Pinky's accessibility service is on. */
    fun serviceOn(): Boolean {
        val am = activity.getSystemService(AccessibilityManager::class.java) ?: return false
        return am.getEnabledAccessibilityServiceList(AccessibilityServiceInfo.FEEDBACK_ALL_MASK)
            .any { it.resolveInfo?.serviceInfo?.packageName == activity.packageName }
    }

    fun openAccessibility() = open(Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS))

    fun openAppInfo() = open(
        Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", activity.packageName, null)),
    )

    fun open(intent: Intent) {
        try {
            activity.startActivity(intent)
        } catch (_: ActivityNotFoundException) {
        }
    }

    // Helpers.

    fun rounded(color: Int, radiusDp: Int): Drawable = GradientDrawable().apply {
        setColor(color)
        cornerRadius = radiusDp * density
    }

    fun matchWrap() = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT)

    /** matchWrap with a gap above. */
    fun below(gapDp: Int) = matchWrap().apply { topMargin = dp(gapDp) }

    private fun clickable(v: View, onClick: () -> Unit) {
        v.background = RippleDrawable(ColorStateList.valueOf(ripple), null, ColorDrawable(Color.WHITE))
        v.isClickable = true
        v.setOnClickListener { onClick() }
    }

    companion object {
        // Type sizes in sp: a little larger than desktop Pinky's, for a phone.
        const val SMALL = 13f
        const val BODY = 15f
        const val HEADLINE = 17f
        const val TITLE2 = 20f
        const val TITLE1 = 28f

        /**
         * Hidden from accessibility services that are not accessibility
         * tools (Pinky's own included) and deaf to the touches they inject
         * (Android 14 and later), with the view's children; touches that
         * come through another app's window are dropped too.
         */
        fun sensitive(v: View) {
            v.filterTouchesWhenObscured = true
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
                v.setAccessibilityDataSensitive(View.ACCESSIBILITY_DATA_SENSITIVE_YES)
            }
        }
    }
}

/** Sets the text when it changed, and hides the view while it is empty. */
fun TextView.say(s: CharSequence?) {
    val t = s ?: ""
    if (text.toString() != t.toString()) text = t
    visibility = if (t.isEmpty()) View.GONE else View.VISIBLE
}

fun View.shown(on: Boolean) {
    visibility = if (on) View.VISIBLE else View.GONE
}
