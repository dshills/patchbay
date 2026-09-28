# AC-3 review — proposal workbench and deck supervision

Prism `039a8caf0aabb4dfef198a8e231790b6` reviewed staged source with
Gemini `gemini-3-flash-preview`. Two low-severity findings were addressed: the
private selection-view helper now documents its runtime-lock requirement, and
selection choices sort by explicit fields. No high or medium findings were returned.

`make check` passed: vet, lint, ordinary/race tests, builds, plugin conformance and
20 Python release-tool tests. Focused follow-up tests cover the real Unix-socket
daemon with fake deck input, full-review pairing, exact five-second hold admission,
selection changes, stale delayed releases, lease expiry, non-resurrecting renewal,
reconnect floors, generic-confirmation rejection and exact-job cancellation.

All three real-browser scenarios passed. The new one reviews an isolated fake
provider's granted action, refreshes identical content while preserving the checkbox,
approves it and observes a real `/bin/echo` result. The existing context fixture
still makes only one generation; script-like output remains plain text. The ordinary
workbench runs without a provider. A version-negotiation regression found while
running a newer bridge against an older daemon was fixed by checking the advertised
supervision capability before calling new endpoints.

The proposal screenshot was inspected. Focus and keyboard controls remain visible;
full JSON previews scroll without losing access to their review/expiry status.
Real provider, physical Stream Deck, DG812/MHO954 and other-platform verification
remain separate, pending gates. No physical equipment was operated for this phase.
