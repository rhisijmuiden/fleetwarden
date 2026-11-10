# Distributieservers (vestigingen)

Een distributieserver is een cache op een vestiging. Elk goedgekeurd
installatiebestand, elk driver- of firmwarebestand en elke agent-update gaat
**één keer** over de (trage) verbinding met de beheerserver; de agents op die
vestiging halen het daarna via het lokale netwerk bij de distributieserver.

```
beheerserver ──één keer per bestand──▶ distributieserver (vestiging) ──LAN──▶ agents
```

- De distributieserver **maakt zelf verbinding** met de beheerserver, op
  hetzelfde adres als de agents (via je reverse proxy of een secure gateway).
  Vanaf internet hoeft niets hem te bereiken.
- Hij bewaart alle geüploade installatiebestanden, de drivers en firmware in
  de repository en de agentpakketten. Bestanden die niet meer worden
  aangeboden, verwijdert hij.
- **Wie wat krijgt, bepaalt nog steeds de beheerserver.** Bij elke download
  stuurt de distributieserver het ondertekende verzoek van de agent door naar
  de beheerserver en levert alleen het bestand dat die noemt; een agent zonder
  openstaande taak voor een bestand krijgt niets, precies zoals zonder
  distributieserver.
- **Agents controleren elk bestand.** Ze vergelijken de grootte en SHA-256 die
  de beheerserver gaf en pinnen het certificaat van de distributieserver (de
  beheerserver geeft de vingerafdruk door), dus een distributieserver kan niets
  anders uitleveren.
- **Agents vallen terug** op de beheerserver als de distributieserver offline
  is, weigert of het bestand nog niet heeft. Een bestand waar een agent om
  vroeg, haalt hij als eerste op.

Wat niet wordt gecachet: Windows Update-inhoud (agents halen die bij
Microsoft), apt-pakketten voor Linux en winget-installatiebestanden, die van
de downloadsites van de leveranciers komen. Alleen bestanden die op de
beheerserver staan, worden verspreid.

## Vereisten

- Een Debian 13-machine (amd64 of arm64) op de vestiging: een kleine VM met 1
  CPU, 1 GB RAM en genoeg schijfruimte voor je installatiebestanden en drivers.
  De pagina toont de totale grootte van wat wordt verspreid.
- De machine bereikt de beheerserver op het adres van de agents.
- De agents op de vestiging bereiken de machine op TCP **8445** (HTTPS).

## Inrichten

1. Open **Administratie > Distributieservers** en kies **Distributieserver
   toevoegen**. Geef een naam en geef aan welke agents hem gebruiken:
   - **Netwerken**: CIDR's zoals `10.20.0.0/16`, of losse adressen. Een agent
     past als een van zijn lokale adressen erin valt, of het publieke adres dat
     de beheerserver ziet; een vestiging achter NAT stel je dus in met haar
     publieke IP (`203.0.113.7`).
   - **Apparaatgroepen**: apparaten in deze groepen gebruiken de server, waar
     ze ook zijn.
   Passen er meerdere servers, dan komt die met het meest specifieke netwerk
   eerst (een agent probeert er maximaal drie).
2. Stel eventueel een **downloadlimiet** in Mbit/s in, zodat het
   synchroniseren de internetverbinding van de vestiging niet volzet.
3. Kopieer de **installatieopdracht** die verschijnt en voer hem als root uit
   op de machine. Die installeert dezelfde versie, meldt aan met het eenmalige
   token (24 uur geldig) en start de dienst. Kwijt? Kies **Nieuwe
   installatieopdracht**.
4. De server wordt **Online** en begint te downloaden. Het adres voor agents
   wordt ingevuld met het adres dat hij doorgeeft (`https://<ip>:8445`); pas
   het aan voor een DNS-naam.

```
curl -fsSL https://github.com/rhisijmuiden/fleetwarden/releases/download/v1.1.0/install-distribution.sh \
  | sudo bash -s -- --server https://fleet.example.com --token fwd_… --version 1.1.0
```

Opties van de installer: `--listen ADRES` (standaard `:8445`),
`--content-dir MAP` om de bestanden op een andere schijf te zetten,
`--min-free-gb N` (standaard 2: hij stopt met downloaden voordat de schijf vol
is), `--deb BESTAND` voor een lokaal pakket.

## Dagelijks gebruik

De pagina toont per server: opgeslagen en nog te downloaden bestanden, wat hij
nu downloadt, schijfruimte, hoeveel agents hem gebruiken, hoeveel bestanden hij
leverde, verzoeken voor bestanden die hij nog niet had en verzoeken die de
beheerserver weigerde, en de laatste melding (elke minuut).

- **Uitschakelen** stuurt de agents terug naar de beheerserver; de bestanden
  blijven staan.
- **Bijwerken naar …** verschijnt als de server een andere versie draait dan de
  beheerserver; hij installeert dezelfde release van GitHub, controleert
  `SHA256SUMS` en zet de oude versie terug als de nieuwe niet start (zoals bij
  de gateway). Je kunt ook de installer opnieuw uitvoeren.
- **Verwijderen** vergeet de server. Haal hem van de machine met
  `apt purge fleetwarden-distribution`.

Op de machine:

| | |
|---|---|
| Configuratie | `/etc/fleetwarden-distribution/distribution.env` |
| Bestanden | `/var/lib/fleetwarden-distribution/content/<sha256>` |
| Logboek | `journalctl -u fleetwarden-distribution` |
| Aanmelding | `sudo fleetwarden-distribution status` |
| Opnieuw aanmelden | `sudo fleetwarden-distribution enroll --server URL --token TOKEN` |

Instellingen in `distribution.env`: `FW_DIST_SERVER`, `FW_DIST_LISTEN`,
`FW_DIST_CONTENT_DIR`, `FW_DIST_MIN_FREE_GB`, `FW_DIST_TLS_CERT` en
`FW_DIST_TLS_KEY` (je eigen certificaat; agents pinnen ook dat),
`FW_DIST_CA_FILE` (een interne CA voor het certificaat van de beheerserver).

## Beveiliging

- De distributieserver heeft een eigen Ed25519-sleutel; elk verzoek aan de
  beheerserver is daarmee ondertekend. Het eenmalige token werkt maar één keer.
- Hij bewaart geen inventaris, wachtwoorden of tokens: alleen de bestanden en
  zijn sleutel.
- De dienst draait als eigen gebruiker met systemd-sandboxing en kan alleen in
  zijn eigen datamap schrijven.
- Een gestolen distributieserver kan de verspreide installatiebestanden lezen
  (net als elke agent met een taak), maar geen bestanden krijgen die de
  beheerserver niet uitgeeft, en niet veranderen wat agents installeren.
