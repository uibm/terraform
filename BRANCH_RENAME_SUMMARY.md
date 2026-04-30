# Branch Rename Summary

## Date: 2026-04-30

## Branches Renamed

### 1. terraform-scan → upstream-merge-2024
**Reason:** This branch only contains upstream merge commits with no custom features. The new name clearly indicates it's an upstream merge from 2024.

**Original commits:**
- Merge pull request #36120 from bschaatsbergen/b/s3-object-lock-file
- Update tfdeploy.mdx (#36143)
- Various upstream merge commits

**New name reflects:** Pure upstream synchronization branch

---

### 2. v6 → feature/docs-command-v6
**Reason:** This branch implements the `terraform docs` command feature on an older upstream base. The new name follows the feature branch naming convention and clearly indicates what it does.

**Key features:**
- Implements `terraform docs` command
- Adds documentation generation functionality
- Based on older upstream version

**Files modified:**
- `commands.go` - Added docs command registration
- `internal/command/docs.go` - Implements docs command
- `.gitignore` - Updated to exclude sensitive files

---

### 3. v7 → feature/docs-command-v7
**Reason:** This branch implements the same `terraform docs` command feature but on a newer upstream base. The new name follows the feature branch naming convention and clearly indicates what it does.

**Key features:**
- Implements `terraform docs` command (same as v6)
- Adds documentation generation functionality
- Based on newer upstream version

**Files modified:**
- `commands.go` - Added docs command registration
- `internal/command/docs.go` - Implements docs command
- `.gitignore` - Updated to exclude sensitive files

---

### 4. terraform-docs-support (unchanged)
**Reason:** This branch name is already descriptive and follows good naming conventions. It clearly indicates it adds support for the docs command.

**Key features:**
- Implements `terraform docs` command
- Most actively developed docs command branch
- Contains comprehensive test coverage

---

### 5. history-support (unchanged)
**Reason:** This is the current working branch for history command support. Name is already descriptive.

**Key features:**
- Implements `terraform history` command
- Adds command history tracking
- Currently active development branch

---

## Branch Naming Convention

All branches now follow this convention:
- **feature/[feature-name]**: Feature development branches
- **[descriptive-name]**: Descriptive names for special purpose branches
- **upstream-merge-[year]**: Upstream synchronization branches

## Command to View All Branches

```bash
git branch -a | grep -E "(feature|upstream|terraform-docs|history)"
```

## Current Branch Structure

```
* history-support                  (current - history command feature)
  feature/docs-command-v6          (docs command on older base)
  feature/docs-command-v7          (docs command on newer base)
  terraform-docs-support           (primary docs command branch)
  upstream-merge-2024              (upstream sync only)
  main                             (base branch)