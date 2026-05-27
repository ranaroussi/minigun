# MiniGun agent skill

A [Factory Droid](https://factory.ai) skill that teaches an AI agent to operate [MiniGun](https://github.com/ranaroussi/minigun) end-to-end — drafting newsletters, dispatching campaigns, managing lists, monitoring sends, cleaning bounces, and keeping deliverability healthy.

This is not a CLI wrapper. It's a full operator playbook: pick the right send mode for the moment, run a pre-send checklist, ramp throttle on cold IPs, walk the user through DMARC graduation, and push back on the anti-patterns that wreck inbox reputation.

## What's in here

```
skill/
├── README.md            # this file
└── minigun/
    └── SKILL.md         # the skill itself — install this
```

`minigun/SKILL.md` is a single self-contained file with YAML frontmatter and ~400 lines of markdown. It covers:

- **Interface selection** — when to prefer MCP tools over the CLI over raw HTTP.
- **The full surface** — every operation mapped across MCP / CLI / HTTP.
- **The send dispatch matrix** — when to use `send_single` (no `list:`) vs `send_bulk` (with `list:`), and why getting it wrong gets you flagged.
- **Markdown authoring** — variable defaults, preheader, the auto-injected unsub footer.
- **The standard operating playbook** — pre-send checklist, post-send polling, failure recovery, when to use `--force` on resume.
- **Mailgun deliverability** — the IP warming schedule, SPF/DKIM/DMARC setup, Postmaster Tools, content red flags.
- **List hygiene** — how the auto-cleanup webhook works, when to use `delete_contact` vs `unsubscribe_contact`.
- **Common recipes** — newsletter dispatch, transactional sends, bounce-list migration, IP warming kickoff.
- **Anti-patterns** — what to push back on when the user asks for something risky.

## Install

### As a personal skill (recommended for operators)

Available across all your projects — every Droid session sees it.

```bash
mkdir -p ~/.factory/skills
ln -s "$(pwd)/skill/minigun" ~/.factory/skills/minigun
```

The symlink is preferable to a copy because the skill stays in sync as the MiniGun repo updates.

If you'd rather copy:

```bash
mkdir -p ~/.factory/skills
cp -r skill/minigun ~/.factory/skills/minigun
```

### As a project skill

Available only when the Droid CLI is invoked from inside the MiniGun repo (or a repo where you've vendored this skill).

```bash
mkdir -p .factory/skills
cp -r skill/minigun .factory/skills/minigun
# or symlink
ln -s "$(pwd)/skill/minigun" .factory/skills/minigun
```

Project skills already living in the repo at `.factory/skills/minigun/` get auto-discovered by Droid when you run `droid` from the repo root — no install step.

### Verify the skill is active

After installing, start a new Droid session and try one of these prompts:

> "I need to send the weekly newsletter to my subscribers."

> "Can you help me clean up the bounced addresses from a list?"

> "Why are my emails going to spam?"

The skill should activate (you'll see it listed in the active skills section) and the agent should respond with MiniGun-specific guidance — naming the MCP tools / CLI commands and walking through the operator checklist.

## Use from other AI clients

The `SKILL.md` file is plain markdown and is not Droid-specific. Any AI client that supports loading external context files can use it.

- **Claude Desktop / Cursor**: Add `skill/minigun/SKILL.md` to your project's context or a custom instructions block.
- **Continue / Zed / Goose**: Same — point at `SKILL.md` as a context file. If the client also speaks MCP, configure the MiniGun MCP server (`minigun mcp`) at the same time so the agent has both the playbook *and* the tools.
- **Plain ChatGPT / Claude.ai web UI**: Paste the contents of `SKILL.md` into a custom GPT's instructions or a Project's context.

The skill assumes the agent has tools available (MCP, CLI, or raw HTTP via an SDK) — without tools, it can still help draft copy, walk the user through deliverability questions, and explain the operator playbook, but it can't actually send anything.

## Pair with the MCP server for full autonomy

For the deepest experience, install both the skill **and** the MiniGun MCP server:

```bash
# 1. Install the CLI (which doubles as an MCP server)
go install github.com/ranaroussi/minigun/cli/cmd/minigun@latest

# 2. Install this skill (see "Install" above)

# 3. Configure your AI client to launch the MCP server.
#    Example for Claude Desktop (~/Library/Application Support/Claude/claude_desktop_config.json):
{
  "mcpServers": {
    "minigun": {
      "command": "minigun",
      "args": ["mcp"],
      "env": {
        "MINIGUN_API_URL": "https://mailer.example.com",
        "MINIGUN_API_TOKEN": "..."
      }
    }
  }
}
```

With both installed, you can give the agent the highest-level instruction and trust the rest:

> "Send the August update to the newsletter list. Use the markdown in `./drafts/aug.md`. Test mode first."

The agent will know to: verify the worker is up, look up the list to confirm subscriber count, read the markdown, check the from-address aligns with a sending domain, run `send_bulk` with `test_mode=true`, poll until done, ask you to confirm the test rendering looks right, and only then run the real send. The skill teaches it that flow; the MCP server gives it the hands.

## Updating the skill

If you installed via symlink, `git pull` in the MiniGun repo updates the skill in place.

If you installed via copy:

```bash
cp -r skill/minigun ~/.factory/skills/minigun
```

The skill is versioned with the MiniGun source — when new endpoints / tools / CLI commands ship, the corresponding SKILL.md section is updated in the same commit. Pull regularly.

## Feedback / contributions

Found a recipe the skill is missing? An anti-pattern that should be added to the pushback list? A deliverability nuance the playbook gets wrong? Open an issue or PR on [the MiniGun repo](https://github.com/ranaroussi/minigun) — skill updates ship in the same release stream as the server.
