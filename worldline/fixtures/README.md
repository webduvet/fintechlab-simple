# Worldline settlement-file fixtures

Drop a real example settlement file here and set
`WORLDLINE_FILE_SOURCE=fixture` on the `worldline` service. Files whose
names carry the slot code are served **byte-for-byte**, with their own
filename preserved:

- `..._ER_....csv` — morning file
- `..._AR_....csv` — afternoon confirmation file

Serving the real bytes is the point: it tests a parser against the genuine
article rather than against this lab's rendering of it. The filename is
kept as-is because a real file's own name is part of what a consumer's
filename validator has to accept.

With `WORLDLINE_FILE_SOURCE=generate` (the default) this directory is
ignored and files are built from the transactions the acquirer holds.

Nothing real belongs in a public repo: scrub merchant identifiers, card
numbers and amounts before committing anything here.
