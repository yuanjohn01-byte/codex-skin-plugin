package guardian

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Registration struct {
	ID         string
	Platform   string
	Executable string
	Arguments  []string
}

type DescriptorRegistrar struct {
	Directory     string
	WindowsUserID string
}

func fixedRegistration(platform, executable string) Registration {
	return Registration{
		ID:         registrationID,
		Platform:   platform,
		Executable: executable,
		Arguments:  []string{"run", "--reason", "process", "--json", "--internal-spike"},
	}
}

func (registrar DescriptorRegistrar) Install(_ context.Context, registration Registration) error {
	if err := validateRegistration(registration); err != nil {
		return err
	}
	if err := ensureDirectory(registrar.Directory); err != nil {
		return err
	}
	filename, content, err := registrar.render(registration)
	if err != nil {
		return err
	}
	return writeBytesAtomic(registrar.Directory, filename, content)
}

func (registrar DescriptorRegistrar) Remove(_ context.Context, registration Registration) error {
	filename, err := registrationFilename(registration.Platform)
	if err != nil {
		return err
	}
	path := filepath.Join(registrar.Directory, filename)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (registrar DescriptorRegistrar) render(registration Registration) (string, []byte, error) {
	filename, err := registrationFilename(registration.Platform)
	if err != nil {
		return "", nil, err
	}
	if strings.HasPrefix(registration.Platform, "macos-") {
		content, err := renderLaunchAgent(registration)
		return filename, content, err
	}
	content, err := renderScheduledTask(registration, registrar.WindowsUserID)
	return filename, content, err
}

func registrationFilename(platform string) (string, error) {
	if strings.HasPrefix(platform, "macos-") {
		return "com.codexskin.guardian.plist", nil
	}
	if platform == "windows-x64" {
		return "CodexSkinGuardian.xml", nil
	}
	return "", fmt.Errorf("%w: unsupported registration platform", ErrConfiguration)
}

func validateRegistration(registration Registration) error {
	if registration.ID != registrationID || !filepath.IsAbs(registration.Executable) {
		return fmt.Errorf("%w: registration identity or executable", ErrRegistration)
	}
	if len(registration.Arguments) != 5 || registration.Arguments[0] != "run" || registration.Arguments[1] != "--reason" || registration.Arguments[2] != "process" || registration.Arguments[3] != "--json" || registration.Arguments[4] != "--internal-spike" {
		return fmt.Errorf("%w: registration arguments are not fixed", ErrRegistration)
	}
	if strings.ContainsAny(registration.Executable, "\r\n\x00") {
		return fmt.Errorf("%w: executable contains control characters", ErrRegistration)
	}
	return nil
}

func renderLaunchAgent(registration Registration) ([]byte, error) {
	if !strings.HasPrefix(registration.Platform, "macos-") {
		return nil, fmt.Errorf("%w: LaunchAgent platform", ErrRegistration)
	}
	values := append([]string{registration.Executable}, registration.Arguments...)
	var arguments strings.Builder
	for _, value := range values {
		arguments.WriteString("    <string>")
		if err := xml.EscapeText(&arguments, []byte(value)); err != nil {
			return nil, err
		}
		arguments.WriteString("</string>\n")
	}
	content := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" +
		"<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n" +
		"<plist version=\"1.0\">\n<dict>\n" +
		"  <key>Label</key>\n  <string>" + registrationID + "</string>\n" +
		"  <key>ProgramArguments</key>\n  <array>\n" + arguments.String() + "  </array>\n" +
		"  <key>RunAtLoad</key>\n  <true/>\n" +
		"  <key>KeepAlive</key>\n  <false/>\n" +
		"  <key>ProcessType</key>\n  <string>Background</string>\n" +
		"  <key>LimitLoadToSessionType</key>\n  <string>Aqua</string>\n" +
		"</dict>\n</plist>\n"
	return []byte(content), nil
}

func renderScheduledTask(registration Registration, userID string) ([]byte, error) {
	if registration.Platform != "windows-x64" || userID == "" || strings.EqualFold(userID, "SYSTEM") || strings.ContainsAny(userID, "\r\n\x00") {
		return nil, fmt.Errorf("%w: Windows user identity", ErrRegistration)
	}
	escape := func(value string) (string, error) {
		var buffer bytes.Buffer
		if err := xml.EscapeText(&buffer, []byte(value)); err != nil {
			return "", err
		}
		return buffer.String(), nil
	}
	executable, err := escape(registration.Executable)
	if err != nil {
		return nil, err
	}
	user, err := escape(userID)
	if err != nil {
		return nil, err
	}
	content := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" +
		"<Task version=\"1.4\" xmlns=\"http://schemas.microsoft.com/windows/2004/02/mit/task\">\n" +
		"  <Triggers><LogonTrigger><Enabled>true</Enabled><UserId>" + user + "</UserId></LogonTrigger></Triggers>\n" +
		"  <Principals><Principal id=\"Author\"><UserId>" + user + "</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>\n" +
		"  <Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><AllowHardTerminate>true</AllowHardTerminate><StartWhenAvailable>true</StartWhenAvailable><RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable><WakeToRun>false</WakeToRun><ExecutionTimeLimit>PT5M</ExecutionTimeLimit><Priority>7</Priority></Settings>\n" +
		"  <Actions Context=\"Author\"><Exec><Command>" + executable + "</Command><Arguments>run --reason process --json --internal-spike</Arguments></Exec></Actions>\n" +
		"</Task>\n"
	return []byte(content), nil
}

func writeBytesAtomic(directory, filename string, content []byte) error {
	temporary, err := os.CreateTemp(directory, ".registration-")
	if err != nil {
		return err
	}
	path := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(path)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	closed = true
	if err := atomicReplace(path, filepath.Join(directory, filename)); err != nil {
		return err
	}
	return syncDirectory(directory)
}
