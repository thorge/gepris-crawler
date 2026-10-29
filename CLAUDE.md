# Projekt-Manifest: gepris-crawler

Crawler für die DFG-GEPRIS-Datenbank (Go) plus OAI-PMH-Server. Speichert Projekte,
Personen und Einrichtungen in MongoDB.

## Git-Regeln

- **Niemals `git push` ohne vorher ausdrücklich zu fragen.** Auch nicht direkt nach
  einem Commit, für den es eine Freigabe gab — Push ist eine eigene Freigabe.
- Commits nur auf ausdrückliche Anweisung.
- Commit-Messages im Oneline-Stil nach Conventional Commits, z. B.
  `fix: handle missing subject areas`, `feat: store person IDs`, `chore: update deps`,
  `docs: ...`, `refactor: ...`, `test: ...`.
- **Niemals** `Co-Authored-By:`- oder sonstige Attributions-Zeilen in Commits oder PRs.

## Upstream

- Origin ist ein eigener GitHub-Fork; das Original liegt unter
  https://jugit.fz-juelich.de/fdm/gepris-crawler. Änderungen sollen später dorthin
  zurückgespielt werden.
- Daher: Diffs klein und upstream-tauglich halten; Code-Stil, Kommentare (Englisch,
  typografische Anführungszeichen “…” wie im Original) und Struktur des Originals
  beibehalten. Diese Datei (`CLAUDE.md`) gehört nicht in den Upstream-MR.
- `cmd/report` ist allgemein und upstream-tauglich; die Berichte in `reports/` (inkl.
  Vergleichsunis) sind CAU-spezifisch und gehören nicht in den Upstream-MR. Berichte
  und Beschreibungen dort sind auf Deutsch, CSV-Format für deutsches Excel.
- Der Commit `PROVISIONAL: Change Mongo DB name` (DB `gepris-new`) ist als provisorisch
  markiert — nicht ohne Rückfrage ändern.

## Entwicklung

- Go ist lokal nicht installiert; bauen/testen per Docker:
  `docker run --rm -v "$PWD":/src -v gepris-gomod:/go/pkg/mod -w /src -e GOFLAGS=-buildvcs=false golang:1.23 sh -c 'gofmt -l .; go build ./... && go test ./...'`
- GEPRIS nicht unnötig belasten (siehe README). Für Tests `CRAWLER_DEBUG=1` mit
  vorbefüllten Cache-Verzeichnissen nutzen, keine Voll-Crawls.
- `go vet` meldet „unkeyed fields“ bei `bson.E` — das ist der bestehende Stil des
  Originals, nicht anfassen.
