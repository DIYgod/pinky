package app.pinky.standalone

import android.app.Activity
import android.os.Bundle
import android.widget.TextView

/** Pinky's own screen: the task, its steps, Stop. */
class TaskActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(TextView(this).apply { text = "Pinky Standalone" })
    }
}
