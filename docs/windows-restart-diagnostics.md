# Windows restart diagnostics

Development candidates containing this feature record the latest Windows restart
worker in `restart/diagnostic.json` beneath the existing Codex Skin data root.
Older candidates do not produce this file. It does not change restart consent,
process identity checks, theme behavior, transaction recovery or offline Restore.

The report is local only and is not uploaded automatically. It contains:

- The restart request ID, plus the version and build commit reported by that worker.
- Fixed stage names, status, elapsed milliseconds and duration milliseconds.
- The first stage to report failure, and fixed error categories (never raw errors).
- Flags for truncated records or earlier diagnostic write failures.

Only one report is retained, with at most 64 stage entries and 16 KiB. Each stage
is recorded before it runs, then updated when it returns. A stage left `running`
is unresolved, not successful. Entries are ordered by start time; enclosing
stages can finish after their child stages. A successful recovery does not turn
the original failed Apply into success.

For investigation, first match `requestId` to the failed request in
`restart/current.json`. Do not associate an older report with a newer attempt.
`interrupted_recovery` can fail before a new Apply operation ID exists.
`stop_process`, `launch_controlled`, `wait_listener`, and `connect_page` distinguish
closing the old process, requesting a controlled launch, reaching its verified
loopback endpoint, and connecting to its page. `failure_recovery`,
`launch_ordinary`, and `wait_ordinary` describe the ordinary-app fallback when
that path is reached. Page connection does not prove a foreground window or a
visually accepted skin.

The report deliberately excludes prompts, code, usernames, absolute paths,
process IDs, ports, URLs, credentials and PowerShell stderr. Missing/incomplete
diagnostics do not authorize retries, manual state deletion or bypassing
recovery checks. A diagnostic write failure does not change the operation's
result; continue using the existing status and recovery records.

## Test boundaries

The portable tests exercise the actual restart worker and interrupted-operation
recovery with synthetic adapters. PowerShell 7 transport checks on macOS and
Windows cross-compilation are not Windows GUI acceptance.

`TestWindowsPowerShellRunsWithoutConsole` uses the actual system PowerShell 5.1
runner on Windows and checks that its child has no console window. It does not
launch, stop, attach to, or modify Codex. Real-app Apply/Switch/Restore acceptance
still requires a separately approved signed candidate and explicit restart
consent when requested.
