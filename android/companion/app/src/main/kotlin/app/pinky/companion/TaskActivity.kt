package app.pinky.companion

import android.app.Activity
import android.content.Intent
import android.graphics.Typeface
import android.graphics.drawable.Drawable
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.text.Editable
import android.text.InputType
import android.text.TextUtils
import android.text.TextWatcher
import android.util.TypedValue
import android.view.Gravity
import android.view.KeyEvent
import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import org.json.JSONArray
import org.json.JSONObject
import java.io.Closeable

/**
 * Pinky's own screen on the phone, in a process of its own (":ui"): the
 * person asks Pinky for a task, follows it and stops it, as in desktop
 * Pinky's Ask Pinky (ui/ask.go). Approvals and handoffs are answered on
 * the card over other apps (Overlays); the model, its key and a task's
 * limits are in SettingsActivity.
 *
 * The agent cannot use this screen: the gateway refuses every action
 * while it is in front and hides what it shows (policy.IsPinkyScreen),
 * OwnUI refuses gestures on it, and its content is sensitive (Look). Only
 * the window's root stays visible to the service, which needs it to see
 * that Pinky is in front.
 */
class TaskActivity : Activity() {

    private val main = Handler(Looper.getMainLooper())
    private lateinit var look: Look
    private lateinit var settings: TaskSettings
    private lateinit var secrets: Secrets
    private var sub: Closeable? = null

    private var state: JSONObject? = null // the core's latest; null while it cannot be reached
    private var down = "" // why the core cannot be reached
    private var notReadySince = 0L // uptime since when Pinky is on but not ready, or 0
    private var serviceOn = false
    private var hasKey = false
    private var starting = false
    private var checkingUpdate = false
    private var update: UpdateCheck.Update? = null

    private lateinit var updateCard: View
    private lateinit var updateTitle: TextView
    private lateinit var updateNotes: TextView
    private lateinit var offCard: View
    private lateinit var coreCard: View
    private lateinit var coreTitle: TextView
    private lateinit var coreDetail: TextView
    private lateinit var setupCard: View
    private lateinit var setupButton: TextView
    private lateinit var runCard: View
    private lateinit var runTask: TextView
    private lateinit var runUsage: TextView
    private lateinit var runWaiting: TextView
    private lateinit var runHint: TextView
    private lateinit var stopButton: TextView
    private lateinit var endCard: View
    private lateinit var endMark: TextView
    private lateinit var endTitle: TextView
    private lateinit var endReason: TextView
    private lateinit var endUsage: TextView
    private lateinit var askBox: View
    private lateinit var input: EditText
    private lateinit var modelLine: TextView
    private lateinit var limitLine: TextView
    private lateinit var startButton: TextView
    private lateinit var error: TextView
    private lateinit var activityHeading: TextView
    private lateinit var stepsGroup: LinearLayout
    private val stepRows = ArrayList<StepRow>()

    private class StepRow(val view: View, val mark: TextView, val text: TextView, val sub: TextView, val person: Drawable?)

    private val semibold = Typeface.create(Typeface.DEFAULT, 600, false)
    private val rerender = Runnable { render() }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        look = Look(this)
        settings = TaskSettings(this)
        secrets = Secrets(this)
        val col = look.column()
        val header = look.header(getString(R.string.app_name))
        header.addView(look.button(getString(R.string.phone_settings), Look.Kind.PLAIN) { openSettings() })
        col.addView(header, look.matchWrap())
        col.addView(updateCard(), look.below(12))
        col.addView(offCard(), look.below(12))
        col.addView(coreCard(), look.below(12))
        col.addView(setupCard(), look.below(12))
        col.addView(runCard(), look.below(12))
        col.addView(endCard(), look.below(12))
        col.addView(askBox(), look.below(12))
        error = look.text("", Look.SMALL, look.red).apply { setPadding(look.dp(4), look.dp(8), look.dp(4), 0) }
        error.say("")
        col.addView(error, look.matchWrap())
        activityHeading = look.section(getString(R.string.phone_activity))
        col.addView(activityHeading, look.matchWrap())
        stepsGroup = look.group()
        col.addView(stepsGroup, look.matchWrap())
        look.setScreen(col)
    }

    override fun onStart() {
        super.onStart()
        sub = CoreClient.subscribe({ st -> main.post { onState(st) } }, { why -> main.post { onDown(why) } })
    }

    override fun onResume() {
        super.onResume()
        serviceOn = look.serviceOn()
        hasKey = secrets.has(settings.provider)
        update = UpdateCheck.offered(this)
        if (UpdateCheck.due(this)) checkUpdate()
        render()
    }

    override fun onStop() {
        sub?.close()
        sub = null
        super.onStop()
    }

    override fun onDestroy() {
        main.removeCallbacksAndMessages(null)
        super.onDestroy()
    }

    // The core.

    private fun onState(st: JSONObject) {
        state = st
        down = ""
        // A service turned off leaves the core running, without its phone.
        if (st.optJSONObject("device")?.optBoolean("ready") != true) serviceOn = look.serviceOn()
        render()
    }

    private fun onDown(why: String) {
        state = null
        down = why
        serviceOn = look.serviceOn()
        render()
    }

    // The screen: at most one of what Pinky needs first (the service, the
    // core, a key and a model), then the task that runs or the one that
    // ended, then the box for a new one.

    private fun render() {
        main.removeCallbacks(rerender)
        val st = state
        val device = st?.optJSONObject("device")
        val ready = serviceOn && device?.optBoolean("ready") == true
        val now = SystemClock.uptimeMillis()
        if (ready || !serviceOn) {
            notReadySince = 0
        } else if (notReadySince == 0L) {
            notReadySince = now
        }
        // The core takes a moment to start: say it failed only after that.
        val startingCore = !ready && serviceOn && now - notReadySince < CORE_GRACE_MS
        if (startingCore) main.postDelayed(rerender, CORE_GRACE_MS - (now - notReadySince))
        val agent = st?.optJSONObject("agent")
        val running = agent?.optBoolean("running") == true
        val setUp = hasKey && settings.model().isNotEmpty()

        val u = update
        updateCard.shown(u != null)
        if (u != null) {
            updateTitle.say(getString(R.string.phone_update_available, u.version))
            updateNotes.say(u.notes)
        }

        offCard.shown(!serviceOn)
        coreCard.shown(serviceOn && !ready)
        if (serviceOn && !ready) {
            coreTitle.say(getString(if (startingCore) R.string.phone_core_starting else R.string.phone_core_failed))
            coreDetail.say(if (startingCore) "" else whyNotReady(device))
        }
        setupCard.shown(ready && !setUp && !running)
        setupButton.text = getString(if (hasKey) R.string.phone_setup_model else R.string.phone_setup_key)

        runCard.shown(running)
        if (agent != null && running) {
            runTask.say(agent.optString("task"))
            runUsage.say(Words.usage(resources, agent))
            val need = st?.optJSONArray("needs")?.optJSONObject(0)
            runWaiting.say(need?.let { getString(R.string.phone_waiting_for, it.optString("title")) })
            runHint.shown(need != null)
        } else {
            look.enable(stopButton, true)
        }

        endCard.shown(agent != null && !running)
        if (agent != null && !running) {
            val (title, reason) = Words.end(resources, agent)
            val end = agent.optString("end")
            endMark.text = if (end == "done") "✓" else "●"
            endMark.setTextColor(
                when (end) {
                    "done" -> look.green
                    "stopped" -> look.ink3
                    else -> look.amber
                },
            )
            endTitle.say(title)
            endReason.say(reason)
            endUsage.say(Words.usage(resources, agent))
        }

        askBox.shown(ready && setUp && !running)
        modelLine.say(settings.modelLabel())
        limitLine.say(
            getString(R.string.phone_task_limit, Words.limit(resources, settings.priced(), settings.maxCost, settings.tokenLimit)),
        )
        updateStart()
        val task = st?.optJSONObject("task")
        renderSteps(task?.optJSONArray("steps") ?: JSONArray(), task?.optString("state").orEmpty())
    }

    private fun whyNotReady(device: JSONObject?): String {
        if (state == null) return down
        return listOf(device?.optString("line").orEmpty(), device?.optString("detail").orEmpty())
            .filter { it.isNotEmpty() }.distinct().joinToString("\n")
    }

    private fun updateStart() {
        look.enable(startButton, input.text.isNotBlank() && !starting)
    }

    // A task.

    private fun startTask() {
        val task = input.text.toString().trim()
        if (task.isEmpty() || starting) return
        starting = true
        error.say("")
        updateStart()
        val provider = settings.provider
        CoreClient.background {
            val why = try {
                val key = secrets.load(provider)
                when {
                    key != null -> {
                        CoreClient.call(settings.startRequest(task, key, provider))
                        null
                    }
                    secrets.has(provider) -> getString(R.string.phone_err_key_lost)
                    else -> getString(R.string.phone_no_key)
                }
            } catch (e: CoreClient.Refused) {
                startError(e)
            } catch (e: Exception) {
                getString(R.string.phone_err_start, e.message ?: e.toString())
            }
            main.post { started(why) }
        }
    }

    private fun started(why: String?) {
        starting = false
        if (why == null) {
            input.setText("")
            // The agent starts from the app the person was in; the Working
            // pill follows the task there.
            moveTaskToBack(true)
        } else {
            error.say(why)
        }
        render()
    }

    private fun startError(e: CoreClient.Refused): String = when (e.code) {
        "no_device" -> getString(R.string.phone_err_no_device)
        "no_key" -> getString(R.string.phone_no_key)
        "no_model" -> getString(R.string.phone_err_no_model)
        "busy" -> getString(R.string.phone_err_busy)
        "no_task" -> "" // Start waits for a task; nothing to say.
        else -> getString(R.string.phone_err_start, e.message ?: e.code)
    }

    private fun stop() {
        look.enable(stopButton, false)
        CoreClient.send(CoreClient.cmd("stop")) { err -> if (err != null) main.post { look.enable(stopButton, true) } }
    }

    private fun dismiss() {
        error.say("")
        CoreClient.send(CoreClient.cmd("dismiss"))
    }

    private fun openSettings() {
        startActivity(Intent(this, SettingsActivity::class.java))
    }

    // Updates, for builds that do not come with desktop Pinky's.

    private fun checkUpdate() {
        if (checkingUpdate) return
        checkingUpdate = true
        val app = applicationContext
        CoreClient.background {
            try {
                UpdateCheck.check(app)
            } catch (_: Exception) {
                // Offline, or no feed: the next visit tries again.
            }
            main.post {
                checkingUpdate = false
                update = UpdateCheck.offered(app)
                render()
            }
        }
    }

    private fun putOffUpdate() {
        update?.let { UpdateCheck.putOff(this, it) }
        update = null
        render()
    }

    private fun downloadUpdate() {
        val u = update ?: return
        look.open(Intent(Intent.ACTION_VIEW, Uri.parse(u.url)))
    }

    // The task's steps, as the core's gateway writes them and desktop
    // Pinky words them (Words.step).

    private fun renderSteps(steps: JSONArray, taskState: String) {
        val n = steps.length()
        activityHeading.shown(n > 0)
        stepsGroup.shown(n > 0)
        if (n < stepRows.size) {
            stepsGroup.removeAllViews()
            stepRows.clear()
        }
        for (i in 0 until n) {
            val s = steps.optJSONObject(i) ?: continue
            val row = stepRows.getOrNull(i) ?: stepRow().also {
                stepRows += it
                look.addRow(stepsGroup, it.view)
            }
            val step = Words.step(resources, s, i == n - 1, taskState)
            row.text.say(step.text)
            row.sub.say(step.sub)
            // What waits for the person is amber, and with the step the
            // agent is on, bold (ui/extras.go).
            row.text.setTextColor(if (step.kind == Words.Kind.WAIT) look.amber else look.ink)
            row.text.typeface = if (step.kind == Words.Kind.WAIT || step.kind == Words.Kind.NOW) semibold else Typeface.DEFAULT
            showMark(row, step.kind, s.optString("status"))
        }
    }

    // A step's mark: what waits, the step the agent is on, the person's own
    // steps (a figure, as in desktop Pinky), and how the others ended.
    private fun showMark(row: StepRow, kind: Words.Kind, status: String) {
        val mark = row.mark
        val figure = if (kind == Words.Kind.YOU) row.person else null
        if (mark.compoundDrawablesRelative[0] !== figure) mark.setCompoundDrawablesRelative(figure, null, null, null)
        val (glyph, color) = when {
            kind == Words.Kind.YOU -> "" to look.ink2
            kind == Words.Kind.WAIT -> "…" to look.amber
            kind == Words.Kind.NOW -> "•" to look.accent
            status == "uncertain" -> "✓" to look.amber
            status in NOT_DONE -> "–" to look.ink3
            status in RAN -> "✓" to look.green
            else -> "✕" to look.red
        }
        if (mark.text.toString() != glyph) mark.text = glyph
        mark.setTextColor(color)
    }

    private fun stepRow(): StepRow {
        val mark = look.text("", Look.BODY, look.ink3, 600)
        val text = look.text("", Look.BODY, look.ink)
        val sub = look.text("", Look.SMALL, look.ink3).apply { setPadding(0, look.dp(2), 0, 0) }
        sub.say("")
        val person = getDrawable(R.drawable.ic_person)?.mutate()?.apply {
            setTint(look.ink2)
            setBounds(0, 0, look.dp(16), look.dp(16))
        }
        val texts = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            addView(text)
            addView(sub)
        }
        val v = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            setPadding(look.dp(14), look.dp(10), look.dp(16), look.dp(10))
            addView(mark, LinearLayout.LayoutParams(look.dp(22), ViewGroup.LayoutParams.WRAP_CONTENT))
            addView(texts, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        }
        return StepRow(v, mark, text, sub, person)
    }

    // The cards.

    private fun updateCard(): View {
        updateTitle = look.text("", Look.BODY, look.ink, 600)
        updateNotes = look.text("", Look.SMALL, look.ink2).apply {
            maxLines = 4
            ellipsize = TextUtils.TruncateAt.END
            setPadding(0, look.dp(4), 0, 0)
        }
        val c = look.card(look.accentTint)
        c.addView(updateTitle)
        c.addView(updateNotes)
        c.addView(
            look.buttons(
                look.button(getString(R.string.phone_update_later), Look.Kind.PLAIN) { putOffUpdate() },
                look.button(getString(R.string.phone_update_download), Look.Kind.PRIMARY) { downloadUpdate() },
            ),
            look.matchWrap(),
        )
        updateCard = c
        return c
    }

    private fun offCard(): View {
        val c = look.card()
        c.addView(look.text(getString(R.string.phone_off_title), Look.HEADLINE, look.ink, 600))
        c.addView(look.text(getString(R.string.phone_off_body), Look.BODY, look.ink2).apply { setPadding(0, look.dp(6), 0, 0) })
        c.addView(
            look.button(getString(R.string.phone_open_accessibility), Look.Kind.PRIMARY) { look.openAccessibility() },
            look.below(14),
        )
        // From Android 13 the setting is restricted for apps installed
        // outside an app store until the person allows it in App info.
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            c.addView(look.text(getString(R.string.phone_restricted), Look.SMALL, look.ink3).apply { setPadding(0, look.dp(14), 0, 0) })
            c.addView(
                look.buttons(look.button(getString(R.string.phone_app_info)) { look.openAppInfo() }).apply {
                    gravity = Gravity.START
                    setPadding(0, look.dp(8), 0, 0)
                },
                look.matchWrap(),
            )
        }
        offCard = c
        return c
    }

    private fun coreCard(): View {
        coreTitle = look.text("", Look.BODY, look.ink, 600)
        coreDetail = look.text("", Look.SMALL, look.ink3).apply { setPadding(0, look.dp(6), 0, 0) }
        coreDetail.say("")
        val c = look.card()
        c.addView(coreTitle)
        c.addView(coreDetail)
        coreCard = c
        return c
    }

    private fun setupCard(): View {
        val c = look.card()
        c.addView(look.text(getString(R.string.phone_setup), Look.BODY, look.ink2))
        setupButton = look.button("", Look.Kind.PRIMARY) { openSettings() }
        c.addView(look.buttons(setupButton), look.matchWrap())
        setupCard = c
        return c
    }

    private fun runCard(): View {
        val top = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
        }
        top.addView(look.text("●", Look.SMALL, look.green).apply { setPadding(0, 0, look.dp(8), 0) })
        top.addView(
            look.text(getString(R.string.phone_task_working), Look.BODY, look.ink, 600),
            LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f),
        )
        stopButton = look.button(getString(R.string.phone_stop), Look.Kind.DANGER) { stop() }
        top.addView(stopButton)
        runTask = look.text("", Look.BODY, look.ink).apply { setPadding(0, look.dp(8), 0, 0) }
        runUsage = look.text("", Look.SMALL, look.ink2).apply { setPadding(0, look.dp(4), 0, 0) }
        runWaiting = look.text("", Look.BODY, look.amber, 600).apply { setPadding(0, look.dp(10), 0, 0) }
        runHint = look.text(getString(R.string.phone_waiting_hint), Look.SMALL, look.ink3).apply { setPadding(0, look.dp(2), 0, 0) }
        val c = look.card()
        c.addView(top, look.matchWrap())
        for (v in listOf(runTask, runUsage, runWaiting, runHint)) c.addView(v, look.matchWrap())
        runCard = c
        return c
    }

    private fun endCard(): View {
        val top = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
        }
        endMark = look.text("", Look.BODY, look.green, 600).apply { setPadding(0, 0, look.dp(8), 0) }
        endTitle = look.text("", Look.BODY, look.ink, 600)
        top.addView(endMark)
        top.addView(endTitle, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        top.addView(look.button(getString(R.string.phone_dismiss), Look.Kind.PLAIN) { dismiss() })
        endReason = look.text("", Look.BODY, look.ink2).apply {
            setPadding(0, look.dp(6), 0, 0)
            setTextIsSelectable(true)
        }
        endUsage = look.text("", Look.SMALL, look.ink3).apply { setPadding(0, look.dp(4), 0, 0) }
        val c = look.card()
        c.addView(top, look.matchWrap())
        c.addView(endReason, look.matchWrap())
        c.addView(endUsage, look.matchWrap())
        endCard = c
        return c
    }

    private fun askBox(): View {
        input = EditText(this).apply {
            // Keeps what the person typed when the screen is made again.
            id = R.id.task_input
            hint = getString(R.string.phone_task_hint)
            setTextColor(look.ink)
            setHintTextColor(look.ink3)
            setTextSize(TypedValue.COMPLEX_UNIT_SP, Look.BODY)
            minLines = 3
            maxLines = 8
            gravity = Gravity.TOP or Gravity.START
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_MULTI_LINE or
                InputType.TYPE_TEXT_FLAG_CAP_SENTENCES
            background = look.rounded(look.groupBg, 14)
            setPadding(look.dp(14), look.dp(12), look.dp(14), look.dp(12))
            addTextChangedListener(object : TextWatcher {
                override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) {}
                override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) {}
                override fun afterTextChanged(s: Editable?) = updateStart()
            })
            // Ctrl+Enter on a keyboard starts, as Cmd+Enter does on a computer.
            setOnKeyListener { _, code, ev ->
                if (code != KeyEvent.KEYCODE_ENTER || !ev.isCtrlPressed) return@setOnKeyListener false
                if (ev.action == KeyEvent.ACTION_DOWN) startTask()
                true
            }
        }
        modelLine = look.text("", Look.SMALL, look.ink2, 500)
        limitLine = look.text("", Look.SMALL, look.ink3)
        val texts = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            addView(modelLine)
            addView(limitLine)
        }
        startButton = look.button(getString(R.string.phone_start), Look.Kind.PRIMARY) { startTask() }
        val row = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(look.dp(4), look.dp(10), 0, 0)
            addView(texts, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
            addView(startButton)
        }
        val box = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        box.addView(input, look.matchWrap())
        box.addView(row, look.matchWrap())
        askBox = box
        return box
    }

    companion object {
        private const val CORE_GRACE_MS = 3_000L

        // Steps that ran.
        private val RAN = setOf("settled", "user", "", "running")

        // Steps that did not happen, without anything having gone wrong
        // (ui/extras.go, stepDisplay).
        private val NOT_DONE = setOf(
            "approval_denied", "stopped", "stale", "expired", "user_took_over", "paused",
            "device_locked", "device_offline", "device_needs_setup", "device_paused",
        )
    }
}
