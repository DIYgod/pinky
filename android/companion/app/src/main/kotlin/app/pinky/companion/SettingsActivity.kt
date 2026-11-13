package app.pinky.companion

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.util.Log
import android.view.View
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import org.json.JSONObject
import java.io.Closeable
import java.io.IOException

/**
 * Settings for Pinky on the phone, in the task screen's process (":ui"),
 * as desktop Pinky has them in Settings › Ask Pinky and Settings ›
 * Approvals (ui/ask.go): the AI model, its key and its address; a task's
 * limits; when Pinky asks; Pinky's settings in Android; its version and
 * updates.
 *
 * The model, its address and the limits are kept here (TaskSettings), the
 * keys in Secrets, each per service. When Pinky asks belongs to the core
 * (policy.Preset), so it changes only while the core runs. Like the task
 * screen, this one is closed to agents (Look).
 */
class SettingsActivity : Activity() {

    private val main = Handler(Looper.getMainLooper())
    private lateinit var look: Look
    private lateinit var settings: TaskSettings
    private lateinit var secrets: Secrets
    private var sub: Closeable? = null

    private var coreUp = false // the core answers, so when Pinky asks can change
    private var backgroundOn = false // the core's state.shell.on
    private var known = false // the core answered, or could not be reached, at least once
    private var preset = 0 // the core's policy.Preset; 0 until it says
    private var checking = false
    private var updateFound: UpdateCheck.Update? = null
    private var updateNote = ""
    private var open: Open? = null // the dialog opened last, to open again with the screen

    private lateinit var serviceRow: Look.Row
    private lateinit var keyRow: Look.Row
    private lateinit var modelRow: Look.Row
    private lateinit var urlRow: Look.Row
    private lateinit var stepsRow: Look.Row
    private lateinit var timeRow: Look.Row
    private lateinit var costRow: Look.Row
    private val presets = ArrayList<Pair<Int, Look.Choice>>()
    private lateinit var presetNote: TextView
    private lateinit var accessibilityRow: Look.Row
    private lateinit var batteryRow: Look.Row
    private lateinit var backgroundRow: Look.Row
    private lateinit var updateRow: Look.Row

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        look = Look(this)
        settings = TaskSettings(this)
        secrets = Secrets(this)
        updateFound = UpdateCheck.offered(this)?.also { updateNote = getString(R.string.phone_update_available, it.version) }

        val col = look.column()
        col.addView(look.header(getString(R.string.phone_settings)), look.matchWrap())

        serviceRow = look.row(getString(R.string.phone_service), desc = getString(R.string.phone_service_desc)) { chooseService() }
        keyRow = look.row(getString(R.string.phone_key), desc = getString(R.string.phone_key_desc)) { keyMenu() }
        modelRow = look.row(getString(R.string.phone_model)) { chooseModel() }
        urlRow = look.row(getString(R.string.phone_url)) { editUrl() }
        addGroup(col, R.string.phone_section_model, serviceRow.view, keyRow.view, modelRow.view, urlRow.view)

        stepsRow = look.row(getString(R.string.phone_steps_limit), desc = getString(R.string.phone_steps_limit_desc)) { chooseSteps() }
        timeRow = look.row(getString(R.string.phone_time_limit), desc = getString(R.string.phone_time_limit_desc)) { chooseTime() }
        costRow = look.row(getString(R.string.phone_cost_limit)) { chooseCost() }
        addGroup(col, R.string.phone_section_limits, stepsRow.view, timeRow.view, costRow.view)

        for ((id, title, desc) in PRESETS) {
            presets += id to look.choice(getString(title), getString(desc)) { setPreset(id) }
        }
        addGroup(col, R.string.phone_section_ask, *presets.map { it.second.view }.toTypedArray())
        presetNote = look.note(getString(R.string.phone_preset_core_down))
        col.addView(presetNote, look.matchWrap())

        accessibilityRow = look.row(getString(R.string.phone_accessibility), desc = getString(R.string.phone_accessibility_desc)) {
            look.openAccessibility()
        }
        val appInfo = look.row(getString(R.string.phone_app_info), desc = getString(R.string.phone_app_info_desc)) {
            look.openAppInfo()
        }
        batteryRow = look.row(getString(R.string.phone_battery), desc = getString(R.string.phone_battery_desc)) {
            if (Battery.unrestricted(this)) Battery.openAppInfo(this) else Battery.askUnrestricted(this)
        }
        backgroundRow = look.row(getString(R.string.phone_bg_title), desc = getString(R.string.phone_bg_row_desc)) {
            look.open(Intent(this, BackgroundActivity::class.java))
        }
        val phoneRows = mutableListOf(accessibilityRow.view, batteryRow.view, backgroundRow.view)
        // Only on phones whose vendor adds the switch; whether it is on cannot be read.
        if (Battery.hasAutostart()) {
            phoneRows += look.row(getString(R.string.phone_autostart), desc = getString(R.string.phone_autostart_desc)) {
                Battery.openAutostart(this)
            }.view
        }
        phoneRows += appInfo.view
        addGroup(col, R.string.phone_section_phone, *phoneRows.toTypedArray())

        val version = look.row(getString(R.string.phone_version), "${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE})")
        updateRow = look.row(getString(R.string.phone_update_check)) { onUpdateRow() }
        addGroup(col, R.string.phone_section_about, version.view, updateRow.view)

        look.setScreen(col)
    }

    override fun onStart() {
        super.onStart()
        sub = CoreClient.subscribe(this, { st -> main.post { onState(st) } }, { main.post { onDown() } })
    }

    // The screen is made anew when it is turned, or its theme or language
    // changes: the dialog open then opens again, with what was typed in its
    // box. Never a key: its box starts again empty.

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        val o = open
        if (o != null && look.dialogOpen()) {
            outState.putString(STATE_OPEN, o.name)
            look.dialogText()?.let { outState.putString(STATE_TEXT, it) }
        }
    }

    override fun onRestoreInstanceState(savedInstanceState: Bundle) {
        super.onRestoreInstanceState(savedInstanceState)
        val o = savedInstanceState.getString(STATE_OPEN)?.let { runCatching { Open.valueOf(it) }.getOrNull() }
        val typed = savedInstanceState.getString(STATE_TEXT)
        when (o) {
            Open.SERVICE -> chooseService()
            Open.KEY_MENU -> keyMenu()
            Open.KEY -> askKey(settings.provider)
            Open.MODEL -> chooseModel(typed)
            Open.URL -> editUrl(typed)
            Open.STEPS -> chooseSteps()
            Open.TIME -> chooseTime()
            Open.COST -> chooseCost()
            null -> {}
        }
    }

    override fun onResume() {
        super.onResume()
        // The person may come back from Android's settings.
        render()
    }

    override fun onStop() {
        sub?.close()
        sub = null
        super.onStop()
    }

    override fun onDestroy() {
        look.dismissDialog()
        main.removeCallbacksAndMessages(null)
        super.onDestroy()
    }

    private fun onState(st: JSONObject) {
        val p = st.optInt("preset", 0)
        val bg = st.optJSONObject("shell")?.optBoolean("on") == true
        if (coreUp && p == preset && bg == backgroundOn) return
        coreUp = true
        known = true
        preset = p
        backgroundOn = bg
        render()
    }

    private fun onDown() {
        if (known && !coreUp) return
        coreUp = false
        known = true
        render()
    }

    private fun render() {
        val p = settings.provider
        serviceRow.value.say(serviceName(p))
        val hasKey = secrets.has(p)
        keyRow.value.say(getString(if (hasKey) R.string.phone_key_saved else R.string.phone_key_add))
        keyRow.value.setTextColor(if (hasKey) look.ink3 else look.accent)
        val model = settings.modelLabel(p)
        modelRow.value.say(model.ifEmpty { getString(R.string.phone_model_none) })
        modelRow.value.setTextColor(if (model.isEmpty()) look.accent else look.ink3)
        modelRow.desc.say(getString(if (settings.claudeList(p)) R.string.phone_model_claude_desc else R.string.phone_model_desc))
        urlRow.value.say(settings.baseUrl(p).ifEmpty { getString(R.string.phone_url_default) })
        urlRow.desc.say(urlDesc(p))

        stepsRow.value.say(Words.steps(resources, settings.maxSteps))
        timeRow.value.say(Words.minutes(resources, settings.maxMinutes))
        costRow.value.say(costLabel(settings.maxCost))
        costRow.desc.say(
            getString(if (settings.priced(p)) R.string.phone_cost_limit_desc else R.string.phone_cost_limit_desc_tokens),
        )

        for ((id, c) in presets) {
            c.radio.isChecked = id == preset
            look.enable(c.view, coreUp)
        }
        presetNote.shown(known && !coreUp)

        accessibilityRow.value.say(getString(if (look.serviceOn()) R.string.phone_on else R.string.phone_off))
        val unrestricted = Battery.unrestricted(this)
        batteryRow.value.say(getString(if (unrestricted) R.string.phone_battery_unrestricted else R.string.phone_battery_optimized))
        batteryRow.value.setTextColor(if (unrestricted) look.ink3 else look.accent)
        backgroundRow.value.say(getString(if (backgroundOn) R.string.phone_on else R.string.phone_off))

        updateRow.desc.say(if (UpdateCheck.enabled) updateNote else getString(R.string.phone_update_dev))
        updateRow.value.say(if (updateFound != null) getString(R.string.phone_update_download) else "")
        updateRow.value.setTextColor(look.accent)
        look.enable(updateRow.view, UpdateCheck.enabled && !checking)
    }

    // The AI model.

    private fun chooseService() {
        open = Open.SERVICE
        val options = listOf(TaskSettings.ANTHROPIC, TaskSettings.OPENAI)
        look.choose(getString(R.string.phone_service), options.map { serviceName(it) }, options.indexOf(settings.provider)) { i ->
            settings.provider = options[i]
            render()
        }
    }

    private fun keyMenu() {
        val p = settings.provider
        if (!secrets.has(p)) {
            askKey(p)
            return
        }
        open = Open.KEY_MENU
        look.menu(
            getString(R.string.phone_key),
            listOf(getString(R.string.phone_key_change), getString(R.string.phone_key_remove)),
        ) { i ->
            if (i == 0) {
                askKey(p)
            } else {
                secrets.forget(p)
                toast(getString(R.string.phone_key_removed_toast))
                render()
            }
        }
    }

    // Adds a key, or changes the saved one.
    private fun askKey(p: String) {
        open = Open.KEY
        val title = getString(if (secrets.has(p)) R.string.phone_key_change else R.string.phone_setup_key)
        look.edit(title, getString(R.string.phone_key_desc), "", getString(R.string.phone_key_hint), Look.Field.SECRET) { v ->
            val key = v.trim()
            // The Keystore may take a moment, the first time above all.
            CoreClient.background {
                val saved = try {
                    secrets.save(p, key)
                    true
                } catch (e: Exception) {
                    // Only its class: its message could say how long the key is.
                    Log.w(TAG, "could not save the key: ${e.javaClass.name}")
                    false
                }
                main.post {
                    toast(getString(if (saved) R.string.phone_key_saved_toast else R.string.phone_key_err_save))
                    render()
                }
            }
        }
    }

    // typed is what the box held when the screen was made anew.
    private fun chooseModel(typed: String? = null) {
        open = Open.MODEL
        val p = settings.provider
        if (!settings.claudeList(p)) {
            look.edit(
                getString(R.string.phone_model),
                getString(R.string.phone_model_desc),
                typed ?: settings.model(p),
                getString(R.string.phone_model_hint),
                Look.Field.TEXT,
            ) { v ->
                settings.setModel(p, v)
                render()
            }
            return
        }
        val models = TaskSettings.CLAUDE_MODELS.toMutableList()
        val cur = settings.model(p)
        // A model set some other way stays on the list.
        if (models.none { it.first == cur }) models += cur to cur
        look.choose(getString(R.string.phone_model), models.map { it.second }, models.indexOfFirst { it.first == cur }) { i ->
            settings.setModel(p, models[i].first)
            render()
        }
    }

    private fun editUrl(typed: String? = null) {
        open = Open.URL
        val p = settings.provider
        look.edit(
            getString(R.string.phone_url),
            urlDesc(p),
            typed ?: settings.baseUrl(p),
            if (p == TaskSettings.OPENAI) "https://api.openai.com/v1" else "https://api.anthropic.com",
            Look.Field.URL,
            { v -> if (TaskSettings.urlOK(v)) null else getString(R.string.phone_url_err) },
        ) { v ->
            settings.setBaseUrl(p, v)
            render()
        }
    }

    private fun serviceName(p: String): String =
        getString(if (p == TaskSettings.OPENAI) R.string.phone_service_openai else R.string.phone_service_anthropic)

    private fun urlDesc(p: String): String =
        getString(if (p == TaskSettings.OPENAI) R.string.phone_url_desc_openai else R.string.phone_url_desc_anthropic)

    // A task's limits.

    private fun chooseSteps() {
        open = Open.STEPS
        chooseLimit(R.string.phone_steps_limit, TaskSettings.STEP_CHOICES, settings.maxSteps, { Words.steps(resources, it) }) {
            settings.maxSteps = it
        }
    }

    private fun chooseTime() {
        open = Open.TIME
        chooseLimit(R.string.phone_time_limit, TaskSettings.MINUTE_CHOICES, settings.maxMinutes, { Words.minutes(resources, it) }) {
            settings.maxMinutes = it
        }
    }

    private fun chooseCost() {
        open = Open.COST
        chooseLimit(R.string.phone_cost_limit, TaskSettings.COST_CHOICES, settings.maxCost, { costLabel(it) }) {
            settings.maxCost = it
        }
    }

    private fun <T> chooseLimit(title: Int, choices: List<T>, cur: T, label: (T) -> String, set: (T) -> Unit) {
        // A value set some other way stays on the list, first.
        val options = if (cur in choices) choices else listOf(cur) + choices
        look.choose(getString(title), options.map(label), options.indexOf(cur)) { i ->
            set(options[i])
            render()
        }
    }

    /** The cost limit as the person reads it: dollars, or tokens for a model without prices (pilot.Config.TokenLimit). */
    private fun costLabel(v: Double): String = Words.limit(resources, settings.priced(), v, (v * 1_000_000).toLong())

    // When Pinky asks.

    private fun setPreset(id: Int) {
        if (!coreUp || id == preset) return
        val was = preset
        preset = id
        render()
        CoreClient.send(CoreClient.cmd("preset", "preset" to id)) { err ->
            // The core's next state says what holds; until then, the old choice.
            if (err != null) {
                main.post {
                    preset = was
                    render()
                }
            }
        }
    }

    // Updates.

    private fun onUpdateRow() {
        updateFound?.let {
            UpdateInstall.startFrom(this, it)
            return
        }
        if (!UpdateCheck.enabled || checking) return
        checking = true
        updateNote = getString(R.string.phone_update_checking)
        render()
        val app = applicationContext
        CoreClient.background {
            var found: UpdateCheck.Update? = null
            val note = try {
                found = UpdateCheck.check(app)
                found?.let { getString(R.string.phone_update_available, it.version) } ?: getString(R.string.phone_update_current)
            } catch (e: Exception) {
                Log.w(TAG, "could not check for updates", e)
                // The feed's own failures are not IOExceptions (UpdateCheck).
                getString(if (e is IOException) R.string.phone_update_offline else R.string.phone_update_failed)
            }
            main.post {
                checking = false
                updateNote = note
                // Offered even when the task screen's card was put off: the person asked.
                updateFound = found
                render()
            }
        }
    }

    // Helpers.

    private fun addGroup(col: LinearLayout, title: Int, vararg rows: View) {
        col.addView(look.section(getString(title)), look.matchWrap())
        val g = look.group()
        for (r in rows) look.addRow(g, r)
        col.addView(g, look.matchWrap())
    }

    private fun toast(s: String) {
        Toast.makeText(applicationContext, s, Toast.LENGTH_SHORT).show()
    }

    // The dialogs, as the screen keeps the one open when it is made anew.
    private enum class Open { SERVICE, KEY_MENU, KEY, MODEL, URL, STEPS, TIME, COST }

    companion object {
        private const val TAG = "PinkyScreens"
        private const val STATE_OPEN = "open_dialog"
        private const val STATE_TEXT = "open_dialog_text"

        // policy.Preset's values, as desktop Pinky lists them.
        private val PRESETS = listOf(
            Triple(1, R.string.phone_preset_commits, R.string.phone_preset_commits_desc), // PresetCommits
            Triple(2, R.string.phone_preset_payments, R.string.phone_preset_payments_desc), // PresetPayments
            Triple(3, R.string.phone_preset_every, R.string.phone_preset_every_desc), // PresetEvery
        )
    }
}
