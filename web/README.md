# web/

seller-inbound ships **no web remote yet**. This directory exists so the
weekly advisory drift job (`.github/workflows/ci.yml`, CLASSIFICATION:
CONTINUOUS DRIFT) can run its `npm ci` + `npx knip --no-exit-code` step
against a real package instead of a missing directory.

When a frontend lands (an MCP-fed console remote, say), it grows here: real
sources, real dependencies, and knip starts reporting unused exports, files
and dependencies for it.
