# Custom Agents

This directory contains role-specific agent definitions for the Lantern project. These agents are Copilot CLI custom agents that provide specialized guidance and validation for specific domains.

## File Format

Each agent is defined as `<AgentName>.agent.md` (kebab-case agent name for code consistency).

### Agent Frontmatter (Required)

```yaml
---
name: AgentName                    # Display name
description: One-line summary of what this agent does and when to use it
argument-hint: "Expected input(s), e.g., 'a component to review' or 'a task to implement'"
tools: [tool1, tool2, ...]        # Optional: explicit tool list (if omitted, all enabled tools allowed)
---
```

### Agent Body

Document the agent's:
- **Mission/Role** — What is this agent responsible for?
- **Scope** — What areas does it cover? What's out of bounds?
- **Process** — How does it approach its domain (e.g., review criteria, validation steps)?
- **Examples** — Concrete examples of the agent's output (Issues filed, recommendations, etc.)
- **Self-Check** — Checklist for the agent to validate its work before conclusion

**Style:** Write in second person ("Your agent does X"), be specific and actionable, avoid generic platitudes.

## Current Agents

### User

**File:** [`User.agent.md`](User.agent.md)

**Role:** UX/UI design review specialist for Lanterns Admin interface

**When to use:**
- Reviewing Admin interface usability and design
- Analyzing user workflows and identifying friction points
- Checking accessibility compliance (WCAG AA)
- Filing UX improvement Issues

**Scope:** Lantern Admin (`admin/` directory) UI/UX only. Does not cover backend behavior, performance, or architectural decisions.

## Adding a New Agent

1. Create `<AgentName>.agent.md` in this directory
2. Include frontmatter with `name`, `description`, `argument-hint`, and optionally `tools`
3. Document the agent's role, scope, process, and examples
4. Reference the agent in the main README if appropriate
5. Commit with message: `docs(agents): add <AgentName> agent`

## Agent Naming Convention

- Use descriptive, role-based names (e.g. `User`, `ArchitectReview`, `SecurityAudit`)
- Store filenames in kebab-case: `<agent-name>.agent.md`
- Use Title Case for the `name` field in frontmatter

## Invoking Agents

```bash
# Use a custom agent with Copilot CLI
copilot --agent User

# Use agent in non-interactive mode
copilot -p "Review the browse page UX" --agent User --allow-all
```

## References

- [Copilot CLI — Custom Agents](https://docs.github.com/copilot/how-tos/copilot-cli)
- [Project README](../../README.md)
- [Lantern AGENTS.md (Technical Architecture)](../../AGENTS.md)
