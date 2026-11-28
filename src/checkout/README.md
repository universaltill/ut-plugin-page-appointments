# src/checkout and src/logbook: what is built, what each function awaits

Pure logic only (slices #7115, #7116). Nothing here is wired to the till or
runs in `main.go`. Binding rules of appointment-v1.md section 6: no deposit
logic, no client name on the basket line or the receipt. Locale keys are in
`locales/en.json` of each package (English only; Weblate translates).

| Function | Awaits (core seam / card) |
|---|---|
| `BuildLines` (basket line spec, ref `appt:<id>:<i>`, no price, quote is display only) | Neutral basket seam: `add_to_basket` from a plugin overlay view with an opaque per-line `ref` (today refused outside `sell.identify`, suggestions.go:163-166; `AddToBasket` has only sku and qty). No card yet: ADR-0221 section 8 item 2. |
| `ParseRef`, `HandleSaleCompleted` (match by ref, complete once, two-step checked_in -> in_service -> completed) | The same seam: core must persist `ref` on the sale line and echo it in `sale.completed` `line_items[]` (`SaleLine` in core ipc.go has no ref). Also `view:sales` consent for the plugin to receive `sale.completed`. |
| `Processor.Store` (`Commit` atomic: appointment, events, outbox) | Guest database host functions, #3157, and the #7113 repository. |
| `Processor.Flush`, `Publisher` (publish, mark only on code 0, republish on next event) | Nothing missing: `event_publish` exists. Needs the plugin to be subscribed to some event to get a "next event". |
| `BuildPayload`, `EventName` | Nothing missing (name pinned to manifest.json by a test). |
| `CompleteByReceipt`, `ReceiptChecker` (manual path, B2) | Core view `sales.receipts.v1` (ADR-0149 section 6) and its consent; the UI to pick a receipt is #7114 territory. Works without the basket seam. |
| `logbook.Parse`, `Consumer.Handle`, `EntryID` | A logbook plugin repo (#3700) with linked mode (#3702): it owns `EntryStore` (unique index on appointment id) and `StaffDirectory`. No such repo exists yet; this package moves there. Needs `events:receive` for the event name above. |
| "unmapped says so" (`KeyUnmapped`) and the mapping screen | #7116 UI and #7113 persistence of `logbook_staff_ref`. |

Deposit display ("already paid, not a tender") is not built: it is #7117,
gated by the legal card.
