**History of** [ADR-0028](../ADR-0028-prime-only-an-idle-reported-five-hour-window.md): text replaced on 2026-11-02.

## Text replaced on 2026-11-02

The opt-in reused the 1.x settings key, which 2.0.0 had left in place, so a
1.x opt-in would have switched prime back on without asking. It moved to a new
key, replacing this Decision bullet:

- `dankaiusage claude-prime` and auto-prime return, opt-in through
  **Claude prime** (`enableClaudePrime`, default off), because the request
  spends a little real usage.
