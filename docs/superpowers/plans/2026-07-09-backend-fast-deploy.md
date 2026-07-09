# Backend Fast Deploy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a fast backend-only deploy mode for ali98 so small Go backend changes do not run frontend checks or page verification.

**Architecture:** Keep the existing deploy script as the single entry point. Add a `--backend-fast` flag that sets skip flags and a backend-focused Go test command before reusing the existing build/upload/health flow.

**Tech Stack:** Bash deploy script, Docker Go build image, shell-based smoke test.

## Global Constraints

- Do not change the default full deployment behavior.
- `--backend-fast` must skip frontend test/build and skip `/purchase`/`/buy` page verification.
- `--backend-fast` must still build and replace the main `sub2api` binary and verify `/health`.

---

### Task 1: Add backend-fast deploy mode

**Files:**
- Modify: `deploy/lklb-deploy-ali98.sh`
- Create: `deploy/test-lklb-deploy-flags.sh`

**Interfaces:**
- Consumes: existing deploy flags `--skip-tests`, `--skip-frontend-build`, `--skip-main-verify`.
- Produces: new flag `--backend-fast`.

- [ ] Step 1: Write a failing shell test that expects `--backend-fast --help` to be accepted and documented.
- [ ] Step 2: Run `bash deploy/test-lklb-deploy-flags.sh`; expected failure before implementation.
- [ ] Step 3: Add `--backend-fast` parsing and usage text.
- [ ] Step 4: Run `bash -n deploy/lklb-deploy-ali98.sh` and `bash deploy/test-lklb-deploy-flags.sh`; expected pass.
- [ ] Step 5: Commit the deploy optimization.
