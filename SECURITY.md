# Security Policy

llaminGO is a local-first desktop/CLI client for a `llama-server` you run. It holds
your chat history and, optionally, an access token for an auth-fronted backend. If
you find a vulnerability, please report it privately so it can be fixed before it's
public.

## Reporting a vulnerability

- **Please do not open a public issue or pull request for security problems.**
- Use GitHub's **private vulnerability reporting** for this repository:
  the **Security** tab → **Report a vulnerability**. This opens a private advisory
  visible only to you and the maintainer.

When reporting, include what you can: affected version, platform, steps to
reproduce, and the impact as you see it.

## What to expect

This is a solo-maintained side project, so there is **no guaranteed response
time** — but security reports are taken seriously and prioritized over feature
work. You'll get an acknowledgement, a discussion of the fix, and credit in the
release notes if you'd like it.

## Supported versions

llaminGO is pre-release and iterating; only the **latest** state of `main` is
supported. Fixes ship forward rather than being backported.

## Good to know

- **Local by default.** The core API binds to loopback (`127.0.0.1`) for the
  desktop webview; v1 does not expose it to the network. Reaching your
  `llama-server` across a LAN or VPN is a connection you secure yourself.
- **Secrets come from the environment or your local config, never the repo.** The
  `llama-server` URL and any bearer token are read from `LLAMINGO_URL` /
  `LLAMINGO_TOKEN` or the saved config under your home directory; none are
  committed, and the repository is kept generic.
- **Tools act with your approval.** The native file tools are confined to a
  project's folder: reads are free, writes and renames are gated (you approve
  them), and deletes re-ask every time. MCP tools run through the same permission
  gate with its risk tiers. Nothing runs that the gate hasn't cleared, and there
  is no plugin system.
- **No arbitrary code execution today.** A guarded shell/command tool is a possible
  future addition; it would be heavily gated, and it is not present now.
