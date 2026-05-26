---
name: commit
description: Create a git commit for staged changes following Conventional Commits specification.
allowed-tools: Bash(git diff:*), Bash(git log:*), Bash(git commit:*)
---

# Git Commit with Conventional Commits

## Instructions

1. Run `git diff --staged` to read the staged changes. If there are no staged changes, inform the user and stop.
2. Run `git log --oneline -5` to understand the recent commit style of the repository.
3. Analyze the staged changes and generate a commit message following the rules below.
4. Execute `git commit` with the generated message.

## Commit Message Rules

### Format (Conventional Commits)
```
<type>[optional scope]: <description>

[optional body]
```

### Types
- `feat`: a new feature
- `fix`: a bug fix
- `docs`: documentation only changes
- `style`: changes that do not affect the meaning of the code (formatting, etc.)
- `refactor`: a code change that neither fixes a bug nor adds a feature
- `perf`: a code change that improves performance
- `test`: adding missing tests or correcting existing tests
- `build`: changes that affect the build system or external dependencies
- `ci`: changes to CI configuration files and scripts
- `chore`: other changes that don't modify src or test files

### Rules
- Write commit messages in **English**
- The `description` must start with a **lowercase** letter
- The `description` must **not** end with a period
- The `description` describes **what was done** (concise, imperative mood)
- The `body` describes **why it was done** (include whenever possible)
- Separate subject from body with a blank line
- Wrap body at 72 characters

### Examples
```
feat(auth): add OAuth2 login support

Users needed a way to sign in with third-party providers without
creating a new account, improving onboarding experience.
```

```
fix: resolve null pointer exception on empty input

The previous implementation did not handle the case where the input
was empty, causing crashes in production.
```

## Execution

Use a HEREDOC to pass the commit message to `git commit`:

```bash
git commit -m "$(cat <<'EOF'
<type>[optional scope]: <description>

[optional body]
EOF
)"
```

After committing, show the result of `git log --oneline -1` to confirm the commit was created successfully.
