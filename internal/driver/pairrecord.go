package driver

// PairRecordFile is where Apple's device service keeps this computer's
// pairing record for an iPhone on Windows. Apple Mobile Device Service writes
// it while an iPhone is paired, and every later session with the phone reads
// it from there.
func PairRecordFile(udid string) string {
	return `C:\ProgramData\Apple\Lockdown\` + udid + ".plist"
}

// PairRecordFixCommand is the command that lets Apple's device service
// replace an iPhone's pairing record, run as an administrator in PowerShell:
// it gives the service (LOCAL SERVICE, S-1-5-19) the right to replace the
// file. The service runs as LOCAL SERVICE, and a record that another install
// of Apple's software wrote, as SYSTEM, is one it may read but not replace,
// so the pairing it saves never lands and the phone refuses every session
// with the record it still holds.
func PairRecordFixCommand(udid string) string {
	return `icacls "` + PairRecordFile(udid) + `" /grant "*S-1-5-19:(F)"`
}
