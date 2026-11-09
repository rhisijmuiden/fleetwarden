# Backup en terugzetten

[English](backup.md)

FleetWarden maakt backups van zijn database en serverbestanden naar een of meer
locaties en zet ze terug vanuit de webinterface, op dezelfde server of op een
nieuwe. Alles staat onder **Administratie > Backup en herstel** (alleen voor
beheerders).

## Wat zit er in een backup

- De PostgreSQL-database: gebruikers, rollen, instellingen, auditlog,
  backuplocaties en in latere fases apparaten, inventaris, beleid en taken.
  Actieve sessies en de backupgeschiedenis gaan niet mee.
- De datamap `/var/lib/fleetwarden`: de geheime sleutel waarmee opgeslagen
  wachtwoorden zijn versleuteld, en geüploade bestanden zoals installers.
- Een kopie van `/etc/fleetwarden/server.env` ter referentie.

## Hoe het werkt

- **Volledig en incrementeel.** Een volledige backup uploadt alles. Een
  incrementele backup knipt de gegevens in blokken op basis van de inhoud en
  uploadt alleen blokken die sinds de laatste volledige backup van dezelfde
  keten zijn veranderd. Elke backup is los terug te zetten; je hoeft nooit zelf
  een keten toe te passen.
- **Versleuteld voordat het de server verlaat.** Elk blok wordt gecomprimeerd
  (zstd) en versleuteld met AES-256-GCM. De sleutel wordt beschermd door je
  wachtwoordzin (Argon2id). De opslagaanbieder ziet alleen willekeurige data.
- **Gecontroleerd bij terugzetten.** Elk blok wordt geverifieerd en de hele
  backup wordt tegen zijn SHA-256-controlesom gecontroleerd voordat er iets
  verandert.
- **Bewaartermijn.** Elke *N* dagen begint een nieuwe volledige backup een
  nieuwe keten; de nieuwste *K* ketens blijven bewaard, oudere worden met hun
  incrementele verwijderd.

## 1. Wachtwoordzin instellen

Open het tabblad **Planning en versleuteling** en kies een wachtwoordzin van
minstens 12 tekens. **Zet hem in je wachtwoordmanager**: zonder kan niemand de
backups terugzetten, ook jij niet. Wijzig je hem later, dan krijgen de backups
op alle bereikbare locaties de nieuwe sleutel.

## 2. Locaties toevoegen

| Type | Voor | Opmerkingen |
| --- | --- | --- |
| SCP / SFTP | NAS (Synology, QNAP), Linux-server, hosting | Wachtwoord of SSH-sleutel. FleetWarden kan een Ed25519-sleutelpaar maken; zet de publieke sleutel in `~/.ssh/authorized_keys` van de backupgebruiker. |
| FTPS | FTP-server met TLS (FileZilla Server, NAS) | Expliciete TLS op poort 21 (standaard) of impliciete TLS op 990. |
| FTP | Alleen oude servers | De backup is versleuteld, het wachtwoord niet. |
| OneDrive | Microsoft 365 | Zie hieronder. |
| Lokale map | Een gekoppelde netwerkshare | Een submap van `/var/lib/fleetwarden/backups`. Koppel de share daar. |

Klik op **Verbinding testen**: FleetWarden maakt verbinding, schrijft en
verwijdert een testbestand en zoekt naar bestaande backups. De eerste keer
toont hij de SSH-hostsleutel of (bij een zelfondertekend FTPS-certificaat) de
vingerafdruk van het certificaat; vergelijk die met de server en klik op
**Vertrouwen**. Verandert de sleutel later, dan stoppen de backups tot je hem
opnieuw vertrouwt.

### Voorbeeld Synology (SFTP)

1. Configuratiescherm > Bestandsservices > FTP: zet **SFTP-service** aan.
2. Maak een gebruiker `fleetwarden-backup` met toegang tot één gedeelde map,
   bijvoorbeeld `backup`.
3. In FleetWarden: type SCP / SFTP, server = adres van de NAS, poort 22,
   gebruiker `fleetwarden-backup`, map `backup/fleetwarden`.

### OneDrive (Microsoft 365)

OneDrive gebruikt een app-registratie met toepassingsmachtigingen, zodat er
geen gebruiker hoeft in te loggen. Je kunt dezelfde app-registratie gebruiken
als voor het aanmelden met Entra.

1. [Entra-beheercentrum](https://entra.microsoft.com) > Toepassingen >
   App-registraties > je FleetWarden-app (of **Nieuwe registratie**).
2. **API-machtigingen** > Een machtiging toevoegen > Microsoft Graph >
   **Toepassingsmachtigingen** > `Files.ReadWrite.All` > Toevoegen. Klik daarna
   op **Beheerderstoestemming verlenen**.
3. **Certificaten en geheimen** > Nieuw clientgeheim; kopieer de waarde.
4. In FleetWarden: type OneDrive, vul tenant-ID, client-ID en geheim in, en de
   user principal name van het account in wiens OneDrive de backups komen
   (bijvoorbeeld een apart `backup@jouwdomein.nl` met een OneDrive-licentie).

`Files.ReadWrite.All` geeft de app toegang tot elke OneDrive in de tenant.
Bewaar het clientgeheim goed en gebruik een aparte app-registratie als je
aanmelden en backups liever gescheiden houdt.

## 3. Planning

Kies hoe vaak (elk uur tot dagelijks), vanaf welk tijdstip, hoe vaak een nieuwe
volledige backup begint en hoeveel volledige backups bewaard blijven.
Voorbeeld: dagelijks om 02:00, elke 7 dagen een volledige backup, 4 bewaren: je
kunt ongeveer vier weken terug. Een backup die gemist is omdat de server uit
stond, draait één keer zodra hij terug is. Mislukt een backup, dan krijgen alle
beheerders met een e-mailadres een bericht (stel eerst de mailserver in).

**Nu backuppen** start meteen een backup; **Nu volledige backup** begint een
nieuwe keten.

## Terugzetten op dezelfde server

1. Tabblad **Terugzetten**, kies de locatie en klik op **Backups zoeken**.
2. Kies een backup, controleer tijdstip en versie, klik op **Deze backup
   terugzetten** en typ `RESTORE`.
3. De server downloadt en controleert de backup, daarna stopt de hulpdienst
   `fleetwarden-restore` de server, zet de database in één transactie terug,
   vervangt de datamap en start de server weer.
4. Iedereen wordt afgemeld. Meld je aan met een account uit de backup.

De vorige toestand blijft tot de volgende terugzetting staan in
`/var/lib/fleetwarden/restore/rollback`. Gaat er iets mis, dan wordt de vorige
toestand automatisch teruggezet. Backups van een nieuwere FleetWarden-versie
kun je niet op een oudere server terugzetten: werk de server eerst bij.

## Terugzetten op een nieuwe server (noodherstel)

1. Installeer FleetWarden op een kale Debian 13-server (zie de README) en meld
   je aan met het eenmalige wachtwoord.
2. Ga naar **Backup en herstel > Terugzetten**, kies **Andere locatie**, vul de
   locatie van de oude server en de backupwachtwoordzin in en klik op
   **Backups zoeken**.
3. Kies de nieuwste backup en zet hem terug.
4. Meld je na de herstart aan met je oude account. Backuplocaties,
   wachtwoordzin en planning komen met de backup mee terug.

De netwerkinstellingen van de nieuwe server (`/etc/fleetwarden/server.env`)
blijven ongewijzigd. Het oude bestand wordt ter vergelijking bewaard als
`/var/lib/fleetwarden/server.env.from-backup`.

## Opdrachtregel

```sh
sudo fleetwarden-server backup                # nu backuppen naar alle ingeschakelde locaties
sudo fleetwarden-server backup --full         # een nieuwe volledige backup beginnen
sudo fleetwarden-server restore-apply         # een in de UI klaargezette terugzetting met de hand uitvoeren
```

`restore-apply` wordt normaal gestart door `fleetwarden-restore.path`; je hebt
het alleen nodig op een systeem zonder systemd.
