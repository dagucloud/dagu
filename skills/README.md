# Bundled Skills

This directory is the source of truth for the skills shipped inside the Dagu binary.

- `dagu/` is the bundled reference skill used by the Dagu agent.
- No example skills are currently bundled with the binary.

`embed.go` lives here because Go's `embed` patterns can only read files inside the package directory tree.
