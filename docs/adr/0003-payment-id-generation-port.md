# Payment ID generation port

The application layer declares gateway-owned ID generator ports and the UUID-backed implementation lives in `internal/uuidgen`. Payment IDs use the public `pay_<uuid>` form, while Bank Operation Keys use the internal `bok_<uuid>` form. Keeping generation behind application ports leaves the domain independent from UUID libraries and gives tests deterministic control over Payment IDs and bank operation keys.
