# Codex CLI integration

Connect Codex CLI to Cortex over stdio MCP.

## 1. Install Cortex

```bash
brew install hurttlocker/cortex/cortex-memory
cortex version
```

Other install options are in [Getting Started](../../docs/getting-started.md).

## 2. Register the MCP server

Add the contents of [`config.toml`](config.toml) to `~/.codex/config.toml`:

```toml
[mcp_servers.cortex]
command = "cortex"
args = ["mcp"]
```

Verify the registration, then start Codex:

```bash
codex mcp list
codex
```

Run `/mcp` in the Codex TUI to confirm `cortex` is connected.

## 3. Add the first directive

Directives are human-authored rules. Add one from the terminal:

```bash
cortex directive add "Always run the project tests before committing"
```

In Codex, ask:

```text
List the active Cortex directives, then make the requested change and follow them.
```

Codex can read the directive with `cortex_directive_list`.

## 4. Record the session outcome

Copy [`AGENTS.md`](AGENTS.md) into the project, or merge its Cortex section into an existing `AGENTS.md`. It tells Codex to call `cortex_ledger_record` at the end of each task with:

- `task_summary`
- `outcome` (`success`, `partial`, or `failure`)
- `files_touched`
- `fix_pattern` when a reusable fix occurred
- `agent_id` and `project` when known

Run a Codex task normally:

```text
Fix the authentication gate regression. Run the tests and record the outcome in Cortex when the task is complete.
```

The ledger is append-only. Codex records outcomes; it does not create directives from them.

## 5. Review proposals

After the same `fix_pattern` appears at least three times in the ledger, scan from the terminal:

```bash
cortex propose scan
cortex propose list
```

Review the evidence, then accept or dismiss the proposal yourself:

```bash
cortex propose accept <id>
# or
cortex propose dismiss <id>
```

Accept and dismiss are CLI-only. Codex can list pending proposals with `cortex_propose_list`, but cannot approve them.
