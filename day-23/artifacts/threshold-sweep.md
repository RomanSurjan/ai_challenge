# Day 23 — threshold sweep

Run: `2026-10-04T17:33:21Z`  
Dataset: `eval/questions.json` (`013e72b5fe3ce770b526ee47e63ec7d17b07aca3e2bcdd89e9ea25f42c2ddbe7`)  
Index: `../day-21/artifacts/index-structural.json`  
Candidate-K: 20, final-K: 5, weights: 0.70 / 0.20 / 0.10

| Threshold | Recall | Precision | Hit@1 | MRR | Avg kept | Rejected | Empty |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 0.30 | 0.458 | 0.340 | 0.700 | 0.820 | 5.00 | 0.000 | 0.000 |
| 0.35 | 0.458 | 0.340 | 0.700 | 0.820 | 5.00 | 0.005 | 0.000 |
| 0.40 | 0.458 | 0.340 | 0.700 | 0.820 | 5.00 | 0.060 | 0.000 |
| 0.45 | 0.458 | 0.370 | 0.700 | 0.820 | 4.70 | 0.160 | 0.000 |
| 0.50 | 0.408 | 0.413 | 0.700 | 0.800 | 4.40 | 0.445 | 0.000 |
| 0.55 | 0.392 | 0.480 | 0.700 | 0.750 | 3.30 | 0.630 | 0.000 |
| 0.60 | 0.325 | 0.480 | 0.600 | 0.650 | 2.00 | 0.870 | 0.200 |

Selected threshold: **0.45**. Rule: highest MRR among thresholds retaining at least 95% of maximum recall; ties: precision, lower empty-context rate, then higher threshold. Sweep is retrieval-only: answer generation was not called.
