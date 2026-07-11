# Cortex memory

- Cortex is available through MCP. At the start of a task, call `cortex_directive_list` and follow active directives.
- At the end of a task, call `cortex_ledger_record` with a short `task_summary`, an `outcome` of `success`, `partial`, or `failure`, the `files_touched`, and a stable `fix_pattern` when the task contains a reusable fix.
- Leave `fix_pattern` empty when no recurring pattern applies. Do not invent one to fill the field.
- Never accept or dismiss a proposal. Those actions are human-only through `cortex propose accept` or `cortex propose dismiss` in the terminal.
