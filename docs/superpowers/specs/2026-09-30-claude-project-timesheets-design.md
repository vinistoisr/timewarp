# Claude project timesheets and reliable startup

Date: 2026-09-30
Status: design approved in conversation, awaiting spec review

## Goal

One database, filled automatically, that gives hours per project per day across every PC, accurate enough to submit QuickBooks Time entries from. More than 90% of the work happens in Claude Code sessions, so Claude time has to be attributed to projects. The tracker has to run at all times and survive reboots without admin rights.

## Background (findings 2026-09-30, DESK63)

- The `\Timewarp` scheduled task was registered to run as `FGROUP\flowadmin`. Task Scheduler refused to start it 10,238 times between 09-27 and 09-30 (event 332). DESK63 recorded no focus events after 2026-07-23.
- Cause: `doInstall()` runs elevated and builds the principal from the elevated process's `USERDOMAIN\USERNAME`. On a PC where the user is not a local admin, UAC elevates as a different account, so the task belongs to that account. The task is then unreadable to the real user, `tray.HasScheduledTask()` returns false, the "Start Automatically?" prompt returns on every manual launch, and accepting it re-registers the task as the admin account again.
- No Defender, AppLocker, CodeIntegrity or application crash events mention timewarp. Nothing was killing the process.
- A standard user can create a scheduled task that runs as themselves without elevation (verified with `schtasks /create` as `vincent`).
- Claude Code window titles are session titles ("✳ Check Granville building 70 electrical data upload"). Only 0 to 1% of terminal and Claude time had a project number from the title regex.
- Each transcript line of type `ai-title` holds `aiTitle` and `sessionId`. The window title is that `aiTitle` prefixed by a status glyph.
- Claude Code deletes transcripts after `cleanupPeriodDays` (45 on DESK63). Attribution data must be captured into the database as work happens.
- Project folders: `S:\PROJECT\<year>\<NN-NNN> <name>`. Proposal folders: `S:\Proposals\...\prop <NN-NNN> <name>`. The two number streams are separate and look identical.

## Part A: startup and install fix

1. `-install` never elevates. It registers the task as the current user (`whoami`-equivalent from the process token, not environment variables) under the name `Timewarp-<username>`, using the existing self-healing task XML (logon trigger plus 5-minute repeat, `IgnoreNew`).
2. `HasScheduledTask()` queries `Timewarp-<username>`. The onboarding prompt no longer needs admin, so it cannot hand the task to another account.
3. The task runs the tracker with the tray icon (no `-silent`).
4. `-install` also merges the Claude hooks (Part B) into `%USERPROFILE%\.claude\settings.json`: it adds its own entries, leaves every other hook untouched, and does not duplicate entries on a second run. It writes a backup of `settings.json` before changing it.
5. `-uninstall` removes the task and only the timewarp hook entries.
6. A legacy `\Timewarp` task owned by another account is reported with the elevated command to delete it (`schtasks /delete /tn Timewarp /f`); the tool does not try to delete it.

## Part B: Claude activity capture

### Hook command

`timewarp.exe -claudehook -dbpath <dir>` reads the hook JSON from stdin, writes rows, and exits 0. It never blocks Claude: registered with `async: true`, it swallows its own errors (logged to `timewarp-hook.log` in the DB folder), and uses a short SQLite busy timeout.

Registered hook events: `UserPromptSubmit`, `PostToolUse` (matcher `.*`), `Stop`, `SessionEnd`.

Fields used from the hook input: `session_id`, `transcript_path`, `cwd`, `hook_event_name`, `prompt`, `tool_name`, `tool_input`.

Session title: the last `ai-title` line in the transcript, read from the tail of the file only (transcripts reach 70 MB).

### Table `claude_events`

| column | type | notes |
|---|---|---|
| id | INTEGER PK | |
| ts | DATETIME | UTC |
| hostname, username | TEXT | |
| session_id | TEXT | |
| session_title | TEXT | may be empty early in a session |
| event | TEXT | `prompt`, `tool`, `stop`, `end` |
| ref_kind | TEXT | `project`, `proposal`, `mention`, `none` |
| ref_number | TEXT | `NN-NNN`, or empty string when none |
| ref_source | TEXT | `path`, `prompt`, `cwd`, or empty string |

One row per distinct number found in the event. An event with no number writes one row with `ref_kind = none` (it still marks the session as active). A unique index on (session_id, ts, event, ref_number) makes re-inserts no-ops; empty strings are used instead of NULL so the index applies.

### Tracker change

The tracker stitches consecutive ticks of the same process into one focus event and keeps only the latest title. Two Claude sessions in tabs of one Windows Terminal window would merge. For Claude processes (WindowsTerminal.exe, claude.exe, Code.exe) the tracker also flushes the pending focus event when the title, with its status glyph removed, changes.

### Number detection

Number pattern: `2\d-\d{3}` bounded by non-digits, so `2026-09-30` does not match.

In order of strength:

1. Path under `S:\PROJECT\<year>\` (either slash direction, any case) whose folder name starts with the number: `project`.
2. Path under `S:\Proposals\` whose folder name contains `prop <number>`: `proposal`.
3. Prompt text `proposal <number>` or `prop <number>`: `proposal`. Prompt text `project <number>`: `project`.
4. Any other bare number in the prompt: `mention`.

Paths are searched in `tool_input` (all string values, recursively, including Bash and PowerShell commands) and in `cwd`.

### Backfill

`timewarp.exe -claudebackfill -dbpath <dir>` walks `%USERPROFILE%\.claude\projects\**\*.jsonl`, and for each transcript emits the same rows the hook would have written: `user` lines with text content as `prompt`, `tool_use` blocks in `assistant` lines as `tool`, the last line of each turn as `stop`. It reads `ai-title` lines for the title. Rows already present (same session_id, ts, event, ref_number) are skipped, so the command is safe to re-run.

## Part C: attribution and the timesheet

### Inputs

All `timewarp-*.db` files in the DB folder (every PC), excluding backups (any name containing `.bak`), tables `focus_events`, `inactivity_periods`, `claude_events`. Folder roots for name lookup are flags with defaults `-projectroot S:\PROJECT` and `-proposalroot S:\Proposals`. Path detection in the hook matches `\PROJECT\<year>\` and `\Proposals\` without relying on the drive letter.

### Rules

1. Idle: an inactivity period of 10 minutes or more is away time. Shorter gaps count as present. Threshold is a flag (`-idle 10m`).
2. Focus intervals while present count for their target:
   - Claude windows (WindowsTerminal.exe, claude.exe, Code.exe): strip the leading status glyph and whitespace from the title, match `session_title` on the same host. If two sessions share the title, use the one with events nearest in time.
   - A generic title ("Claude", "Claude Code") or no match: the session on that host with an event in the preceding 2 minutes, else Unassigned.
   - Other windows: the existing `project_number` from the title, else `Other: <process>`.
3. Session busy intervals: from each `prompt` event to the following `stop` event, split wherever consecutive events are more than 10 minutes apart. These count whether or not the user is present or focused.
4. A session's active time is the union of its busy intervals and its present focus intervals. It is split at each event and assigned to the session's current number: the most recent event that named one, preferring `project` over `proposal` over `mention` within an event. Time before the first number goes to the first number. A session that never names a number goes to `Unassigned: <session title>`.
5. Bare `mention` numbers resolve to a project if a folder `S:\PROJECT\<year>\<number> *` exists, else to a proposal if a proposal folder has that number, else Unassigned.
6. Proposal time is reported under the billing code `Admin`. Notes list each proposal number with its folder name.
7. Per billing code per day, hours are the length of the union of all intervals assigned to it, across sessions, apps and PCs. Different billing codes are summed independently, so a day's total can exceed 24 hours.
8. Rounding: nearest 0.5 h per row; any row with 10 minutes or more and under 0.5 h rounds up to 0.5 h. Rows under 10 minutes are listed but not rounded up.

### Output

- `timewarp.exe -timesheet -from YYYY-MM-DD -to YYYY-MM-DD [-csv file]`
- MCP tool `get_timesheet(date_from, date_to)` returning the same rows as JSON.

Columns: date, billing code (project number or `Admin`), name (project folder name; both names where a number has two folders), raw hours, rounded hours, notes (session titles and main window titles for that day, proposals for Admin rows).

A separate Unassigned section lists session titles and hours. A per-day line shows clock hours (union of all present and busy time) next to the project total.

Nothing is submitted to QuickBooks Time automatically.

## Repository and rollout

- All code in `github.com/vinistoisr/timewarp`. Commits: install fix, hook command, backfill, attribution and timesheet with MCP tool, README. Push after the user confirms.
- README gains a "new PC" section: `git pull`, build, `timewarp.exe -install -dbpath "<OneDrive folder>"`, `timewarp.exe -claudebackfill -dbpath "<OneDrive folder>"`.
- DESK63 has no Go toolchain. Build with a portable Go zip in a temp folder.
- DESK63 interim task `Timewarp-vincent` (registered 2026-09-30 with `-silent`) is replaced by the task from `-install`.

## Testing

Go unit tests, written before the code:

- Number detection: real project and proposal paths, both slash directions, commands containing paths, dates, prompts with `proposal`/`project`/bare numbers.
- Title matching with each status glyph.
- Attribution: parallel sessions on different projects both count; same project from two sources counts once; idle of 10 minutes or more excluded; unattended busy turns count; turns split at 10-minute event gaps; Admin grouping; mention resolution.
- Rounding rules.
- Hook merge into a copy of a real `settings.json`: existing hooks unchanged, idempotent, uninstall removes only timewarp entries.
- Install XML principal uses the token user, not environment variables.

Acceptance: after backfill on DESK63 and LAP20, a September 2026 timesheet from both databases, reviewed by the user.

## Out of scope

- Accumulating `-mcp` processes (nine running on DESK63, some from 09-23). Noted for a later fix.
- Moving the database out of OneDrive.
- Automatic QuickBooks Time entry.
