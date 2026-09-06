package session

import (
	"encoding/base64"
	"encoding/binary"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// Test real inherited ACLs instead of interpreting Windows mode bits as POSIX.
// The test directory must be within the current user's private profile.
func TestWindowsSessionACL(t *testing.T) {
	jar, err := Empty()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "session.json")
	for i := 0; i < 2; i++ {
		if err := Save(path, jar); err != nil {
			t.Fatal(err)
		}
		script := `$ErrorActionPreference='Stop'; $ProgressPreference='SilentlyContinue';
$p=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('` + base64.StdEncoding.EncodeToString([]byte(path)) + `'));
$acl=Get-Acl -LiteralPath $p;
$rules=$acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier]);
foreach($r in $rules) {
  if($r.AccessControlType -eq 'Allow' -and ($r.FileSystemRights -band [Security.AccessControl.FileSystemRights]::ReadData) -and $r.IdentityReference.Value -in @('S-1-1-0','S-1-5-11','S-1-5-32-545','S-1-5-32-546')) { throw 'Session readable by a broad user group' }
}; 'acl-ok'`
		var data []byte
		for _, unit := range utf16.Encode([]rune(script)) {
			data = binary.LittleEndian.AppendUint16(data, unit)
		}
		out, err := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-OutputFormat", "Text", "-EncodedCommand", base64.StdEncoding.EncodeToString(data)).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "acl-ok") {
			t.Fatalf("ACL verification: %v %s", err, out)
		}
	}
}
