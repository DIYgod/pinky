package app.pinky.companion

import android.content.Context
import org.json.JSONObject

/**
 * How Pinky on the phone runs a task: the model's service, model and
 * address, each kept per service, and a task's limits. The keys are in
 * Secrets. Desktop Pinky's Settings › Ask Pinky has the same choices
 * (ui/ask.go), and the core fills in the same defaults (pilot.Normalize).
 */
class TaskSettings(context: Context) {

    private val prefs = context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    /** ANTHROPIC or OPENAI. */
    var provider: String
        get() = if (prefs.getString("provider", null) == OPENAI) OPENAI else ANTHROPIC
        set(v) = prefs.edit().putString("provider", if (v == OPENAI) OPENAI else ANTHROPIC).apply()

    /** The model the person set for a service, or "". */
    fun modelSet(p: String = provider): String = prefs.getString("model_$p", null).orEmpty()

    /** The model a task uses: the one set, else Anthropic's default; "" when an OpenAI-compatible service has none. */
    fun model(p: String = provider): String = modelSet(p).ifEmpty { if (p == ANTHROPIC) DEFAULT_MODEL else "" }

    fun setModel(p: String, v: String) {
        prefs.edit().putString("model_$p", v.trim()).apply()
    }

    /** The service's address the person set, or "" for its default. */
    fun baseUrl(p: String = provider): String = prefs.getString("base_url_$p", null).orEmpty()

    fun setBaseUrl(p: String, v: String) {
        prefs.edit().putString("base_url_$p", cleanUrl(v)).apply()
    }

    /** Whether Settings offers Claude's models to choose from rather than a name to type: Anthropic's own API. */
    fun claudeList(p: String = provider): Boolean = p == ANTHROPIC && baseUrl(p).isEmpty()

    var maxSteps: Int
        get() = prefs.getInt("max_steps", DEFAULT_STEPS).takeIf { it > 0 } ?: DEFAULT_STEPS
        set(v) = prefs.edit().putInt("max_steps", v).apply()

    var maxMinutes: Int
        get() = prefs.getInt("max_minutes", DEFAULT_MINUTES).takeIf { it > 0 } ?: DEFAULT_MINUTES
        set(v) = prefs.edit().putInt("max_minutes", v).apply()

    var maxCost: Double
        get() = prefs.getFloat("max_cost", DEFAULT_COST.toFloat()).toDouble().takeIf { it > 0 } ?: DEFAULT_COST
        set(v) = prefs.edit().putFloat("max_cost", v.toFloat()).apply()

    /** Whether Pinky knows the model's prices, and so counts the cost limit in dollars (pilot.Config.Priced). */
    fun priced(p: String = provider): Boolean = p == ANTHROPIC && PRICED.any { model(p).startsWith(it) }

    /** The cost limit in tokens, for a model whose prices Pinky does not know (pilot.Config.TokenLimit). */
    val tokenLimit: Long get() = (maxCost * 1_000_000).toLong()

    /** The model as the person reads it: a Claude model's name, or the name the service gave. */
    fun modelLabel(p: String = provider): String {
        val m = model(p)
        if (p == ANTHROPIC) CLAUDE_MODELS.firstOrNull { it.first == m }?.let { return it.second }
        return m
    }

    /** The core's start command for a task on a service, with its key. */
    fun startRequest(task: String, key: String, p: String = provider): JSONObject = CoreClient.cmd(
        "start",
        "task" to task,
        "key" to key,
        "provider" to p,
        "model" to model(p),
        "base_url" to baseUrl(p),
        "max_steps" to maxSteps,
        "max_minutes" to maxMinutes,
        "max_cost" to maxCost,
    )

    companion object {
        const val ANTHROPIC = "anthropic"
        const val OPENAI = "openai"
        const val DEFAULT_MODEL = "claude-opus-5-5"
        private const val PREFS = "task"
        private const val DEFAULT_STEPS = 100
        private const val DEFAULT_MINUTES = 20
        private const val DEFAULT_COST = 2.0

        /** The models Settings offers for Anthropic's own API, the most capable first (ui/ask.go, claudeModels). */
        val CLAUDE_MODELS = listOf(
            "claude-opus-5-5" to "Claude Opus 5.5",
            "claude-sonnet-5-5" to "Claude Sonnet 5.5",
            "claude-haiku-5-5" to "Claude Haiku 5.5",
        )

        /** The limits Settings offers (ui/ask.go). */
        val STEP_CHOICES = listOf(25, 50, 100, 200, 500)
        val MINUTE_CHOICES = listOf(5, 10, 20, 30, 60)
        val COST_CHOICES = listOf(0.5, 1.0, 2.0, 5.0, 10.0)

        // The models whose prices Pinky knows (internal/pilot/price.go).
        private val PRICED = listOf(
            "claude-opus-5-5", "claude-opus-5", "claude-sonnet-5-5", "claude-sonnet-5", "claude-fable-5-2", "claude-haiku-5-5",
        )

        /** An address as it is kept: trimmed, without a trailing slash. */
        fun cleanUrl(v: String): String = v.trim().trimEnd('/')

        /** Whether an address may be saved: empty for the default, else http or https. */
        fun urlOK(v: String): Boolean {
            val u = cleanUrl(v)
            return u.isEmpty() || u.startsWith("https://") || u.startsWith("http://")
        }
    }
}
