# Updates en kanalen

FleetWarden heeft twee updatekanalen. Engelse versie: [updates.md](updates.md).

| Kanaal | Volgt | Releases |
| --- | --- | --- |
| Productie | `main` | `vX.Y.Z` |
| Test | `test` | pre-releases `vX.Y.Z-test.N` |

Kies het kanaal onder **Administratie > Instellingen > Updates** of in het
venster **Over**. Alleen nieuwere versies worden aangeboden: het
databaseschema gaat alleen vooruit, dus overstappen van Test naar Productie
wacht tot Productie een nieuwere versie heeft dan de versie die je draait.

## Wat "Updaten naar …" doet

De webserver draait als de gewone gebruiker `fleetwarden` en kan geen pakketten
installeren. Hij schrijft de gevraagde versie naar
`/var/lib/fleetwarden/update/request.json`; de unit `fleetwarden-update.path`
start dan `fleetwarden-update.service` (root), die:

1. de release van GitHub downloadt en controleert tegen `SHA256SUMS`,
2. een backup maakt van de database (`pg_dump`), `/etc/fleetwarden` en de
   geheime sleutel in `/var/lib/fleetwarden-update/backups/` (de laatste 3
   blijven bewaard),
3. het pakket installeert en wacht tot de server antwoordt met de nieuwe versie,
4. als dat mislukt: de database terugzet, de vorige versie opnieuw installeert
   en "teruggedraaid" meldt.

Het verzoekbestand bevat alleen een versienummer; de updater accepteert niets
anders en downloadt alleen uit de officiële repository. De voortgang staat in
`/var/lib/fleetwarden-update/status.json` en `journalctl -u fleetwarden-update`.
Beheerders met een e-mailadres krijgen een mail met het resultaat.

## Versies van Debian-pakketten

Testversies worden verpakt als `X.Y.Z~test.N`; Debian sorteert die onder de
release `X.Y.Z`, dus een productierelease vervangt altijd een testversie van
hetzelfde versienummer.
