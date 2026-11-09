# Belastingtest

FleetWarden is gebouwd voor 100 tot 1.000 endpoints. Vóór versie 1.0 is de
server getest met **2.000 gesimuleerde agents**, het dubbele van de bovengrens,
om de grenzen te vinden en op te lossen. Engelse versie:
[load-test.md](load-test.md).

## Het hulpmiddel

`fleetwarden-loadtest` (in `cmd/fleetwarden-loadtest`) simuleert agents met het
echte protocol: inschrijven met een Ed25519-sleutel, de ondertekende
WebSocket-sessie, een volledige inventaris (150 programma's) als de server
erom vraagt, en elke minuut een heartbeat met de schijfvolumes. Tegelijk logt
het in op de webinterface en meet het de pagina's die een beheerder het vaakst
opent. Aan het eind verbreekt het alle sessies tegelijk, zoals bij een
herstart van de server, en meet het hoe snel elke agent terug is.

```sh
make loadtest
# Alleen op een testserver: de gesimuleerde apparaten verschijnen in de apparatenlijst.
sudo fleetwarden-server enrollment-token --group Default
dist/linux-amd64/fleetwarden-loadtest -server https://em.voorbeeld.nl -token fwe_... \
  -agents 2000 -ramp 2m -duration 10m -api-user admin -api-password '...' -report resultaat.json
```

De sleutels van de gesimuleerde agents staan in `loadtest-agents.json`, zodat
een tweede run dezelfde apparaten opnieuw verbindt. Verwijder de gesimuleerde
`LOADTEST-*`-apparaten en het bestand daarna met
`fleetwarden-loadtest -cleanup -server ... -api-user admin -api-password '...'`.

## Opzet

- Eén machine met **4 CPU-cores en 15 GB RAM** waarop de server, PostgreSQL 16
  *én* de lastgenerator draaien, dus de cijfers zijn aan de voorzichtige kant.
- 2.000 Windows-agents die in 2 minuten verbinden, daarna 5 minuten stabiel,
  daarna een herverbindingsstorm.
- De kwetsbaarheidsbronnen waren gesynchroniseerd, en elk gesimuleerd apparaat
  draait dezelfde ongepatchte Windows-build: **2.678 open CVE's op elk van de
  2.000 apparaten (5,4 miljoen matches)**. Dat is een worst case; een gepatcht
  park heeft er veel minder.

## Resultaten

| | Vóór de fixes | Na de fixes |
|---|---|---|
| Verbonden agents | 2.000 van 2.000 | 2.000 van 2.000 |
| Mislukte of verbroken sessies | 2 van 200 in een eerste kleine run (zie hieronder) | 0 |
| Geheugen van de server (RSS) met 2.000 agents | 3,1 GB | 0,2 GB |
| Alle agents terug na een herverbindingsstorm | 3,8 s | 2,5 s |
| Verzendtijd heartbeat p95 | 1,0 ms | 0,4 ms |
| Kwetsbaarheden-samenvatting p50 / max | 2,9 s / 5,8 s | 1 ms (cache) / 0,7 s |
| Kwetsbaarhedenlijst (500 CVE's, server in rust) | 10,7 s | 1,6 s |
| Kwetsbaarhedenlijst (100 CVE's, tijdens de test) | 10,8 s | 3,1 s |
| Apparatenlijst, dashboards, beveiliging, patches p95 | onder 50 ms | onder 25 ms |
| Geïnstalleerde software (alle programma's) p95 | 0,46 s | 0,49 s |

De database groeide naar 1,7 GB, vooral kwetsbaarheidsmatches en inventarissen.

## Wat is opgelost

1. **Agents konden hun verbinding verbreken** als de server twee berichten
   tegelijk stuurde (bij elke verbinding sturen meerdere onderdelen tegelijk
   hun stand). De berichten konden in een andere volgorde in de wachtrij komen
   dan ze genummerd waren; de agent weigert een lager volgnummer en verbindt
   opnieuw. Ondertekenen en in de wachtrij zetten is nu één stap per
   verbinding.
2. **3 GB geheugen voor WebSocket-compressie.** Elke agentverbinding hield een
   eigen compressor vast (ongeveer 1,3 MB). De server spreekt nu compressie
   zonder context takeover af, waarbij compressors gedeeld worden; bestaande
   agents accepteren dat zonder update.
3. **Kwetsbaarheden-samenvatting en -lijst** telden unieke apparaten over
   miljoenen matches. Ze brengen de matches nu eerst terug tot één regel per
   CVE, tellen apparaten met één indexopzoeking per apparaat en berekenen de
   exacte aantallen alleen voor de CVE's op de pagina. De samenvatting wordt 30
   seconden bewaard en ververst zodra matching of een geaccepteerd risico haar
   verandert.
4. **Database-instellingen**: de connectiepool is 20 verbindingen (was één per
   CPU-core) en de JIT-compiler van PostgreSQL staat uit voor de verbindingen
   van FleetWarden, wat ongeveer 0,7 s per dashboardquery scheelde. Zet
   `pool_max_conns=N` of `jit=on` in `FW_DATABASE_URL` om dat te overschrijven.
5. **Indexen**: een index op het apparaat-id als tekst (gebruikt door elk
   agentbericht) en ruimte op elke tabelpagina zodat heartbeat-updates
   goedkoop blijven.
6. De systemd-unit staat 65.536 open bestanden toe.

## Dimensionering

| Endpoints | Server | Opmerking |
|---|---|---|
| tot 250 | 2 vCPU, 4 GB RAM, 40 GB schijf | |
| 250 tot 1.000 | 4 vCPU, 8 GB RAM, 80 GB schijf | de aanbevolen grootte |
| 1.000 tot 2.000 | 4 tot 8 vCPU, 8 tot 16 GB RAM, 150 GB schijf | getest, zie hierboven |

Schijfruimte gaat vooral naar de softwarebibliotheek en de driverrepository die
je uploadt, en naar de backups die op de server blijven. Geef PostgreSQL op
grotere installaties een kwart van het geheugen als `shared_buffers`.

## Bekende grenzen

- **Zoeken in de kwetsbaarhedenlijst** van een park met miljoenen open matches
  duurt een paar seconden als de zoekterm op de meeste matches past
  (bijvoorbeeld "windows"). Filteren op ernst, KEV, groep of apparaat is snel.
- **Geïnstalleerde software** toont elk programma van elk apparaat in één
  overzicht; met 2.000 apparaten en elk 150 programma's duurt dat ongeveer een
  halve seconde.
- De test simuleert agents, niet de taken die ze uitvoeren (patchscans,
  software-installaties, configuraties); die spreidt de planner over de tijd.
