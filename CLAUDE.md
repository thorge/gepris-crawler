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

## Ziele

- Schwerpunkt dieses Forks sind **Auswertungen und Berichte** über die GEPRIS-Daten
  (z. B. Uni-Vergleiche, Kennzahlen zu Verfahren). OAI-PMH ist für uns nebensächlich.
- Bei Entscheidungen haben Auswertbarkeit und Berichte Vorrang vor Upstream-Tauglichkeit.

## Verhältnis zum Original (Jülich)

- Origin ist ein eigener GitHub-Fork; das Original liegt unter
  https://jugit.fz-juelich.de/fdm/gepris-crawler. Ob unsere Änderungen dort gemerged
  werden, ist offen.
- **Funktionen des Originals nur erweitern, nie kaputt machen.** Konkret:
  - Der PMH-Server (`cmd/pmh`) muss unverändert funktionieren und dieselbe Ausgabe
    liefern.
  - Bestehende Felder der `projects`-Collection (Namen, Typen, Bedeutung) bleiben
    erhalten; neue Felder, Collections und Programme kommen hinzu.
  - `lastModification` ändert sich nur bei PMH-relevanten Änderungen, sonst würden
    Harvester unnötig alles neu abholen.
  - Bestehende Umgebungsvariablen und das Standardverhalten des Crawlers bleiben;
    Neues ist zusätzlich oder per Umgebungsvariable abschaltbar.
- Code-Stil, Kommentare (Englisch, typografische Anführungszeichen “…”) und Struktur
  wie im Original, damit ein Merge möglich bleibt. Größere Umbauten sind erlaubt,
  wenn eine Auswertung sie braucht — aber nicht auf Vorrat.
- `cmd/report` ist allgemein; die Berichte in `reports/` (inkl. Vergleichsunis) sind
  CAU-spezifisch, auf Deutsch, CSV-Format für deutsches Excel. Weder `reports/` noch
  diese Datei gehören in einen etwaigen Upstream-MR.
- Der Commit `PROVISIONAL: Change Mongo DB name` (DB `gepris-new`) ist als provisorisch
  markiert — nicht ohne Rückfrage ändern.

## Entwicklung

- Go ist lokal nicht installiert; bauen/testen per Docker:
  `docker run --rm -v "$PWD":/src -v gepris-gomod:/go/pkg/mod -w /src -e GOFLAGS=-buildvcs=false golang:1.23 sh -c 'gofmt -l .; go build ./... && go test ./...'`
- GEPRIS nicht unnötig belasten (siehe README). Für Tests `CRAWLER_DEBUG=1` mit
  vorbefüllten Cache-Verzeichnissen nutzen, keine Voll-Crawls.
- `go vet` meldet „unkeyed fields“ bei `bson.E` — das ist der bestehende Stil des
  Originals, nicht anfassen.
