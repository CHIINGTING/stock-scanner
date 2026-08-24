---
name: scanner-master-design
description: Location and role of the canonical Scanner architecture document
metadata: 
  node_type: memory
  type: reference
  originSessionId: 2ab912aa-bcc1-490d-ae4a-f03d45b96782
---

The canonical architecture / design document for the Scanner project lives at `docs/SCANNER_MASTER_DESIGN.md` (in-repo). It is the permanent design reference — read it first when picking up Scanner work in a new session.

Sections: Project Goal, Non Goals, Design Principles, Data Source, Scoring System (A composite 0–100, B BestFourPoint 0–5, C blended Action, D Rocket 0–100, E Sector), Stage Analysis (RocketStage / RotationStage / ShortTermFlowStage), Trend / Volume / Relative Strength / Industry Strength / Breakout Detection, Result Structure, plus Future Roadmap, Technical Debt, Open Questions.

Maintenance rule stated in the doc: **update it whenever program behaviour changes.** It documents "current implementation" only; speculative items go to Roadmap/Open Questions. Related: [[scanner-direction]].
