# Security Policy

GoKG is a local-first developer tool that reads Go source code, writes a local BadgerDB graph, and can expose that graph through MCP over stdio or local HTTP.

## Supported Versions

Until the first stable release, security fixes are made on `main` and included in the next alpha tag.

| Version | Supported |
|---|---|
| `main` | Yes |
| `v0.x` alpha releases | Best effort |

## Reporting a Vulnerability

Please do not open a public issue for a suspected vulnerability.

Use GitHub private vulnerability reporting for this repository when it is enabled. If private reporting is unavailable, contact the maintainer through the GitHub profile linked from the repository owner account.

Helpful details include:

- A short description of the issue and impact.
- Steps to reproduce.
- A minimal repository or code sample, if one is needed.
- The `gokg version --json` output.
- Whether the issue affects stdio MCP, HTTP MCP, graph parsing, workspace storage, or release artifacts.

You can expect an initial acknowledgement within 7 days for actionable reports.

## HTTP MCP Exposure

`gokg mcp --http` binds to `127.0.0.1:8080` by default and does not provide built-in authentication. Treat it as a local trusted-client endpoint. Do not bind it to a public interface unless another trusted layer provides access control.
