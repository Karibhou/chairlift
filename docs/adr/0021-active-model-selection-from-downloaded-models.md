# 0021 — Active Model selection from downloaded llmman models

- **Status:** Accepted
- **Date:** 2026-10-11

## Context

PR #568 removed ChairLift's curated model-family preset picker, which
attempted to resolve fitting Hugging Face models, download them, and configure
the `bluefin-active` alias in `llmman`. That change replaced the picker with a
"Manage Models" link to llmman's loopback web UI (`http://127.0.0.1:17434/#/models`)
and made the Active Model row read-only.

However, users who pull models using `llmman pull` or `llmman run` in a terminal
found that selecting among their downloaded models in ChairLift required opening
the browser interface, even though `llmman` already exposes locally stored models
over the loopback `/llmman/node` endpoint.

## Decision

1. **Active Model is an interactive selector**: The Active Model row is built as
   an `AdwComboRow` populated dynamically from `/llmman/node`'s `stored` map
   via `NodeStatus.Models()`.
2. **llmman owns model downloads**: ChairLift does not download models or curate
   hard-coded model families. The "Manage Models" row remains in place to direct
   users to llmman's web UI for pulls, removal, and chat.
3. **In-app alias configuration**: When the user selects a model from the
   dropdown, ChairLift sets `aliases.bluefin-active` via `llmman config set` and
   restarts the loopback service so the selection immediately takes effect for
   Goose and local API clients.
4. **Re-entry safety**: Programmatic updates to the ComboRow during state
   restoration or refresh hold an explicit suppression flag (`agentModelSuppress`)
   and maintain `agentModelActive` to prevent recursive event loops and
   unintended service restarts.

## Consequences

- Users can switch between models they have already downloaded directly from the
  Control Center GUI without opening a browser.
- ChairLift writes only the `bluefin-active` alias to llmman's config, leaving
  all model storage, downloads, and engine management to llmman.
- The Manage Models link remains accessible for discovering and pulling new
  models.
