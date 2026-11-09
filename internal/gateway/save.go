package gateway

import (
	"bufio"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Files for the agent: phone_observe { save } writes a screenshot, and
// phone_release { export } the task's report, into the phone's folder on
// this computer (FilesDir), where an agent on this computer reads them and
// one on another computer downloads them through a one-time link
// (internal/mcp). They stay on this computer otherwise. What they show is
// what the agent may see: a screenshot is taken and covered as for
// phone_observe { screenshot: true }, and the report's screenshots are
// those of the task's history, covered alike. The task's screen recording
// is left out: it does not cover one-time codes or blocked apps'
// notifications, and catches a blocked app only when a read sees it, so it
// stays with the person, who can export it from Activity. Saving asks
// nothing, also for read-only agents and in read-only mode: the phone is
// not touched.

// SavedFile is a file a call wrote for the agent on Pinky's computer.
type SavedFile struct {
	Path string // where it is, absolute
	Name string // its name in the phone's folder
	Size int64
}

// shotExts are the kinds of picture a screenshot is saved as.
var shotExts = []string{".png", ".jpg", ".jpeg"}

// shotName cleans the name a screenshot is saved as, a path inside the
// phone's folder: the rules of uploads, and .png when it names no kind.
func shotName(p string) (name, why string) {
	name, why = uploadName(p)
	if why != "" {
		return "", why
	}
	ext := strings.ToLower(path.Ext(name))
	switch {
	case ext == "":
		name += ".png"
	case !contains(shotExts, ext):
		return "", "a screenshot is saved as .png or .jpg"
	}
	return name, ""
}

// saveFile writes rel in dir, the phone's folder on this computer, through
// a temporary file, so a failed write leaves nothing half written and a
// link in the way is replaced, not followed. A file of that name is
// replaced, and replaced says so.
func saveFile(dir, rel string, write func(io.Writer) error) (f SavedFile, replaced bool, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return f, false, err
	}
	old, why, err := uploadTarget(dir, rel)
	switch {
	case err != nil:
		return f, false, err
	case why != "":
		return f, false, fmt.Errorf("%s cannot be saved: %s", rel, why)
	}
	dst, err := localPath(dir, rel)
	if err != nil {
		return f, false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".pinky-*")
	if err != nil {
		return f, false, err
	}
	defer os.Remove(tmp.Name())
	w := bufio.NewWriter(tmp)
	err = write(w)
	if err == nil {
		err = w.Flush()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return f, false, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return f, false, err
	}
	fi, err := os.Stat(dst)
	if err != nil {
		return f, false, err
	}
	return SavedFile{Path: dst, Name: rel, Size: fi.Size()}, old != nil, nil
}

// saveShot saves a screenshot the agent may see as rel in the phone's
// folder, at full resolution: a PNG, or a JPEG for a .jpg name. It adds the
// step that says so to the task, and returns the message for the agent.
func (g *Gateway) saveShot(l *lease, rel string, img image.Image) (SavedFile, string, error) {
	enc := func(w io.Writer) error { return png.Encode(w, img) }
	if ext := strings.ToLower(path.Ext(rel)); ext == ".jpg" || ext == ".jpeg" {
		enc = func(w io.Writer) error { return jpeg.Encode(w, img, &jpeg.Options{Quality: 92}) }
	}
	f, replaced, err := saveFile(g.FilesDir(l.dev.id), rel, enc)
	if err != nil {
		return SavedFile{}, "", err
	}
	g.addStep(l, StepView{Tool: "phone_observe", Args: argsJSON(map[string]any{"save": rel}),
		Text: "Saved a screenshot as " + quoteName(rel), Status: string(StatusSettled), At: time.Now()})
	b := img.Bounds()
	verb := "Saved the screenshot as"
	if replaced {
		verb = "Saved the screenshot over"
	}
	return f, fmt.Sprintf("%s %s (%d×%d, %s).", verb, rel, b.Dx(), b.Dy(), SizeText(f.Size)), nil
}

// exportStepText is the step phone_release { export } adds to a task.
const exportStepText = "Saved the task's report for the agent"

// taskExport is a task phone_release { export } saves the report of.
type taskExport struct {
	task, device, dir string
	video             *taskVideo // its recording, if it was recording
}

// exportForAgent saves the reports of tasks that just ended in their
// phones' folders on this computer, once their screenshots and steps are
// written, and tells res where. It does not wait for their recordings,
// which it leaves out.
func (g *Gateway) exportForAgent(ctx context.Context, exports []taskExport, res *Result) {
	g.shots.wait()
	g.rec.flush()
	now := time.Now()
	for _, e := range exports {
		on := ""
		if len(exports) > 1 {
			on = " of the task on " + e.device
		}
		fail := func(err error) {
			res.Message += fmt.Sprintf(" Saving the report%s failed: %v.", on, err)
		}
		if err := os.MkdirAll(e.dir, 0o700); err != nil {
			fail(err)
			continue
		}
		base := uniqueBase(e.dir, "pinky-task-"+now.Format("2006-01-02-150405"))
		out, err := g.exportTo(ctx, []string{e.task}, e.dir, base, now, false)
		if err != nil {
			fail(err)
			continue
		}
		files := make([]SavedFile, 0, 2)
		for _, p := range []string{out.html, out.json} {
			fi, err := os.Stat(p)
			if err != nil {
				continue
			}
			files = append(files, SavedFile{Path: p, Name: filepath.Base(p), Size: fi.Size()})
		}
		res.Saved = append(res.Saved, files...)
		res.Message += fmt.Sprintf(" Saved the report%s as %s (its steps, with the screenshots Pinky kept) and %s (the same as data).",
			on, filepath.Base(out.html), filepath.Base(out.json))
		if e.video != nil {
			if _, ok := e.video.view(); ok {
				res.Message += " Its screen recording stays on Pinky's computer, as it does not cover one-time codes: the person can add it with Export… in the task's ⋯ menu in Activity."
			}
		}
	}
}
