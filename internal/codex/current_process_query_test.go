package codex

import "testing"

// Execute the source-embedded production query through the real transport.
// Only the three system data sources are replaced; no desktop is touched.
func TestWindowsPowerShellCurrentProcessQuery(t *testing.T) {
	const executable = `C:\Program Files\WindowsApps\OpenAI.Codex_fixture\app\ChatGPT.exe`
	const fixtures = `
$script:codexFixturePath = 'C:\Program Files\WindowsApps\OpenAI.Codex_fixture\app\ChatGPT.exe'
function New-CodexFixtureRow([int]$rowId, [string]$line) {
  [pscustomobject]@{ ProcessId=$rowId; ExecutablePath=$script:codexFixturePath; CommandLine=$line; CreationDate='2026-09-06T00:00:00Z' }
}
function Get-Process { param([int]$Id) [pscustomobject]@{ Path=$script:codexFixturePath } }
function Get-AuthenticodeSignature { param([string]$LiteralPath) [pscustomobject]@{ Status='Valid' } }
`
	for _, fixture := range []struct {
		name string
		rows string
		ids  []int
	}{
		{"empty", "", nil},
		{"main_only", `New-CodexFixtureRow 4242 'ChatGPT.exe'`, []int{4242}},
		{"main_then_renderer", "New-CodexFixtureRow 4242 'ChatGPT.exe'\nNew-CodexFixtureRow 4243 'ChatGPT.exe --type=renderer'", []int{4242}},
		{"renderer_then_main", "New-CodexFixtureRow 4243 'ChatGPT.exe --type=renderer'\nNew-CodexFixtureRow 4242 'ChatGPT.exe'", []int{4242}},
		{"children_only", "New-CodexFixtureRow 4243 'ChatGPT.exe --type=renderer'\nNew-CodexFixtureRow 4244 'ChatGPT.exe --type=utility'", nil},
		{"multiple_mains_preserved", "New-CodexFixtureRow 4242 'ChatGPT.exe'\nNew-CodexFixtureRow 4243 'ChatGPT.exe --type=renderer'\nNew-CodexFixtureRow 4245 'ChatGPT.exe'", []int{4242, 4245}},
		{"child_space_and_case", "New-CodexFixtureRow 4243 'ChatGPT.exe --TYPE utility'\nNew-CodexFixtureRow 4242 'ChatGPT.exe'", []int{4242}},
		{"different_executable_ignored", `[pscustomobject]@{ ProcessId=9999; ExecutablePath='C:\SyntheticOtherApp.exe'; CommandLine='OtherApp.exe'; CreationDate='2026-09-06T00:00:00Z' }`, nil},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			prefix := fixtures + "\nfunction Get-CimInstance {\n" + fixture.rows + "\n}\n"
			var rows []struct {
				ProcessID    int    `json:"processId"`
				Path         string `json:"path"`
				CommandLine  string `json:"commandLine"`
				CreationDate string `json:"creationDate"`
				SignerStatus string `json:"signerStatus"`
			}
			err := runTestPowerShellJSON(t, prefix+windowsIdentityScript(t, "DiscoverCurrentInstance"), []string{executable}, &rows)
			if err != nil || len(rows) != len(fixture.ids) {
				t.Fatalf("current process query: err=%v, rows=%d; want %d", err, len(rows), len(fixture.ids))
			}
			for i, row := range rows {
				if row.ProcessID != fixture.ids[i] || row.Path != executable || row.CommandLine != "ChatGPT.exe" || row.CreationDate != "2026-09-06T00:00:00Z" || row.SignerStatus != "Valid" {
					t.Fatalf("current process query did not preserve synthetic main row %d", i)
				}
			}
		})
	}
}
