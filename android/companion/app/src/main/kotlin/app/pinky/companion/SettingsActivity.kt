package app.pinky.companion

import android.app.Activity
import android.content.Intent
import android.net.Uri
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
    private var known = false // the core answered, or could not be reached, at least once
    private var preset = 0 // the core's policy.Preset; 0 until it says
    private var checking = false
    private var updateFound: UpdateCheck.Update? = null
    private var updateNote = ""

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

        stepsRow = look.row(getString(R.string.phone_steps_limit), desc = getString(R.string.phone_steps_limit_desc)) {
            chooseLimit(R.string.phone_steps_limit, TaskSettings.STEP_CHOICES, settings.maxSteps, { Words.steps(resources, it) }) {
                settings.maxSteps = it
            }
        }
        timeRow = look.row(getString(R.string.phone_time_limit), desc = getString(R.string.phone_time_limit_desc)) {
            chooseLimit(R.string.phone_time_limit, TaskSettings.MINUTE_CHOICES, settings.maxMinutes, { Words.minutes(resources, it) }) {
                settings.maxMinutes = it
            }
        }
        costRow = look.row(getString(R.string.phone_cost_limit)) {
            chooseLimit(R.string.phone_cost_limit, TaskSettings.COST_CHOICES, settings.maxCost, { costLabel(it) }) {
                settings.maxCost = it
            }
        }
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
        addGroup(col, R.string.phone_section_phone, accessibilityRow.view, appInfo.view)

        val version = look.row(getString(R.string.phone_version), "${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE})")
        updateRow = look.row(getString(R.string.phone_update_check)) { onUpdateRow() }
        addGroup(col, R.string.phone_section_about, version.view, updateRow.view)

        look.setScreen(col)
    }

    override fun onStart() {
        super.onStart()
        sub = CoreClient.subscribe({ st -> main.post { onState(st) } }, { main.post { onDown() } })
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
        if (coreUp && p == preset) return
        coreUp = true
        known = true
        preset = p
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

        updateRow.desc.say(if (UpdateCheck.enabled) updateNote else getString(R.string.phone_update_dev))
        updateRow.value.say(if (updateFound != null) getString(R.string.phone_update_download) else "")
        updateRow.value.setTextColor(look.accent)
        look.enable(updateRow.view, UpdateCheck.enabled && !checking)
    }

    // The AI model.

    private fun chooseService() {
        val options = listOf(TaskSettings.ANTHROPIC, TaskSettings.OPENAI)
        look.choose(getString(R.string.phone_service), options.map { serviceName(it) }, options.indexOf(settings.provider)) { i ->
            settings.provider = options[i]
            render()
        }
    }

    private fun keyMenu() {
        val p = settings.provider
        if (!secrets.has(p)) {
            askKey(p, getString(R.string.phone_setup_key))
            return
        }
        look.menu(
            getString(R.string.phone_key),
            listOf(getString(R.string.phone_key_change), getString(R.string.phone_key_remove)),
        ) { i ->
            if (i == 0) {
                askKey(p, getString(R.string.phone_key_change))
            } else {
                secrets.forget(p)
                toast(getString(R.string.phone_key_removed_toast))
                render()
            }
        }
    }

    private fun askKey(p: String, title: String) {
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

    private fun chooseModel() {
        val p = settings.provider
        if (!settings.claudeList(p)) {
            look.edit(
                getString(R.string.phone_model),
                getString(R.string.phone_model_desc),
                settings.model(p),
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

    private fun editUrl() {
        val p = settings.provider
        look.edit(
            getString(R.string.phone_url),
            urlDesc(p),
            settings.baseUrl(p),
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
            look.open(Intent(Intent.ACTION_VIEW, Uri.parse(it.url)))
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

    companion object {
        private const val TAG = "PinkyScreens"

        // policy.Preset's values, as desktop Pinky lists them.
        private val PRESETS = listOf(
            Triple(1, R.string.phone_preset_commits, R.string.phone_preset_commits_desc), // PresetCommits
            Triple(2, R.string.phone_preset_payments, R.string.phone_preset_payments_desc), // PresetPayments
            Triple(3, R.string.phone_preset_every, R.string.phone_preset_every_desc), // PresetEvery
        )
    }
}
