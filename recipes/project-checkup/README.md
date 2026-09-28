# Project Checkup

Map a project and two existing exec actions: a check and a test. Commands are fully configured locally; they may build files or run tests, so inspect their effective preview. Choose actions without required inputs (or with declared defaults). Nothing is probed or installed. The workflow continues to the second action after a first-action failure. Save a baseline, make a change yourself, capture again and compare elapsed time and per-step status. These times include process/provider overhead and are not a statistical benchmark. Captures are partial when any action fails.
