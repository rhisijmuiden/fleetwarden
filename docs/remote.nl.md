# Overnemen op afstand

*English: [remote.md](remote.md)*

Met FleetWarden neem je vanuit de browser het scherm, de muis en het
toetsenbord over van Windows-servers en -clients. Naast de agent hoeft er
niets geïnstalleerd te worden: de service (`FleetWardenAgent`, SYSTEM) start
bij een sessie een desktophelper in de consolesessie en stopt die weer als de
sessie eindigt.

## Een sessie starten

- **Overnemen op afstand** in het menu toont de online Windows-apparaten met
  een zoekveld, de sessies die nu lopen en het sessielogboek.
- Op de pagina van een apparaat opent **Verbinden** dezelfde viewer.

De viewer opent in een eigen browsertabblad. De werkbalk heeft:

- **Monitor**: één monitor tegelijk, of alle monitoren samen.
- **Kwaliteit**: laag, middel of hoog (lager is vloeiender op trage lijnen).
- **Ctrl+Alt+Del**, en een paneel **Speciale toetsen** voor Alt+Tab, Win, Win+R,
  Win+L, Ctrl+Esc, Ctrl+Shift+Esc, Alt+F4 en Print Screen.
- **Klembord**: tekst die op de andere machine gekopieerd wordt, verschijnt
  hier; Ctrl+V in de viewer plakt je eigen klembord op de andere machine;
  **Typen** typt een tekst als toetsaanslagen (voor velden die plakken
  blokkeren, zoals het aanmeldscherm).
- **Scherm verversen**, **Passend in venster** en **Volledig scherm** (dan gaan
  toetsen als Alt+Tab en de Windows-toets naar de andere machine).
- **Sessie beëindigen**.

Het toetsenbord stuurt scancodes, dus de toetsenbordindeling van de andere
machine geldt, net als bij een echt toetsenbord. Het aanmeldscherm, het
vergrendelscherm en UAC-vragen werken ook: de helper volgt het bureaublad
dat de invoer krijgt.

Alleen gebruikers met het recht **Apparaten op afstand overnemen** (`remote.control`;
de rollen Beheerder en Technicus hebben het) kunnen sessies starten of
beëindigen.

## Terminal

Windows- en Linux-apparaten hebben ook een **Terminal**: een shell in de
browser, zonder SSH of RDP. Die opent in een eigen tabblad vanaf de
apparaatpagina of vanuit **Op afstand overnemen**.

- **Linux en Proxmox**: de loginshell van root (uit `/etc/passwd`, meestal
  bash) op een pseudoterminal, met `TERM=xterm-256color` en de taal van het
  systeem. Schermvullende programma's zoals `htop`, `nano`, `vim` en `mc`
  werken.
- **Windows**: PowerShell, of Opdrachtprompt (te kiezen in de werkbalk), als
  SYSTEM op een ConPTY-pseudoconsole.

De werkbalk kopieert de selectie (Ctrl+Shift+C; op Windows ook Ctrl+C als er
tekst geselecteerd is) en plakt (Ctrl+Shift+V; op Windows ook Ctrl+V), en
maakt de tekst kleiner of groter. Als de shell stopt (`exit`), blijft de
uitvoer staan en start **Nieuwe sessie** een volgende.

Een terminal heeft volledige rechten op het apparaat en vraagt daarom een
eigen recht, **Een terminal openen op apparaten** (`remote.terminal`;
Beheerder en Technicus). De aangemelde gebruiker wordt niet gevraagd, wat de
toestemmingsinstelling van de groep ook is: er wordt niets van het scherm
getoond, net als bij SSH. Elke terminalsessie staat in het sessielogboek en
het auditlog, net als een schermsessie. Het tabblad sluiten of **Sessie
beëindigen** hangt de shell op (SIGHUP op Linux, na 3 seconden SIGKILL; op
Windows worden de shell en zijn console beëindigd).

Een apparaat draait maximaal 3 terminals en 2 schermsessies tegelijk.

## Toestemming

Per apparaatgroep bepaalt **Overnemen op afstand** wat de aangemelde
gebruiker ziet:

| Instelling | Wat er gebeurt |
| --- | --- |
| Automatisch (servers direct, werkplekken vragen), de standaard | Servers en hypervisors: direct. Werkplekken: vragen. |
| Direct overnemen | De sessie start zonder melding. |
| Gebruiker melden | De sessie start; de gebruiker ziet bovenaan het scherm een balk met **Sessie beëindigen**. |
| Gebruiker vragen | De gebruiker krijgt een pop-up met de naam van de beheerder en moet binnen 30 seconden op **Toestaan** klikken; anders wordt de sessie geweigerd. Tijdens de sessie staat de balk in beeld. |

Als niemand is aangemeld, start een sessie direct (er is niemand om het te
vragen). Is er wel iemand aangemeld maar start de pop-up-app niet, dan wordt
een sessie met *vragen* geweigerd (`consent_unavailable`).

## Sessielogboek

Elke sessie wordt vastgelegd met het apparaat, de beheerder, het IP-adres, de
toestemmingsmodus, de duur, de verstuurde data en hoe de sessie eindigde
(bijvoorbeeld *beëindigd door de beheerder*, *geweigerd door de gebruiker*,
*beëindigd door de gebruiker*, *geen antwoord*). Begin en einde staan ook in
het auditlog. Het logboek wordt 400 dagen bewaard.

## Beveiliging en netwerk

- De browser praat alleen met de server, via HTTPS op poort 443. De agent
  opent per sessie een aparte, ondertekende WebSocket; de server stuurt die
  door. Achter een reverse proxy moeten WebSocket-upgrades doorgegeven worden
  (zie [reverse-proxy.md](reverse-proxy.md)).
- Het deel tussen agent en server is bovenop TLS nog eens versleuteld met
  X25519 + AES-256-GCM, met een nieuwe sleutel per sessie.
- De named pipe van de helper accepteert alleen de agentservice (SYSTEM).
- Een agent draait maximaal 6 sessies tegelijk; een sessie heeft één viewer.

## Ctrl+Alt+Del

Windows accepteert een software-Ctrl+Alt+Del van services alleen als het
beleid
`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System\SoftwareSASGeneration`
dat toestaat. De agent zet het de eerste keer op `1` (services), of op `3` als
het `2` was (toegankelijkheidsapps). Een groepsbeleid *Software Secure
Attention Sequence uitschakelen of inschakelen* dat iets anders zegt gaat
voor; zet dat op *Services* (of *Services en toegankelijkheidstoepassingen*).

## Directe verbinding

Zodra het scherm er is, vraagt de viewer de agent om een directe verbinding:
een WebRTC-datakanaal tussen de browser en het apparaat. Als de browser het
apparaat kan bereiken (hetzelfde netwerk, een VPN, of door NAT heen met een
STUN-server), gaan het scherm en de invoer daarover en toont de werkbalk
**Direct**; anders blijft de sessie gewoon **Via server**. Valt de directe
verbinding weg, dan gaat de sessie zonder onderbreking via de server verder.

- Het aanbod en het antwoord gaan over de versleutelde sessieverbinding, dus
  de DTLS-sleutels van het datakanaal zijn van begin tot eind gecontroleerd;
  het datakanaal zelf is versleuteld met DTLS.
- **Instellingen > Overnemen op afstand** zet het uit (alle sessies via de
  server) en neemt optionele STUN-servers (`stun:host:poort`). Zonder
  STUN-servers worden alleen de eigen adressen van het apparaat geprobeerd.
  Er wordt geen TURN-server gebruikt: waar geen directe route is, is de relay
  via de server de terugval.
- Het Windows-installatieprogramma voegt twee inkomende firewallregels toe
  voor UDP naar `fleetwarden-agent.exe`, alleen op domein- en privénetwerken.

## Ontwerpkeuzes

Het plan noemde WebRTC met H.264. Beide paden sturen in plaats daarvan
dezelfde gewijzigde tegels van 64×64 als JPEG: dat werkt door elke proxy en
firewall die HTTPS doorlaat, heeft geen codec op de machines nodig, en doordat
elk beeld bevestigd wordt blijft de vertraging laag op trage lijnen. Het
directe pad haalt de omweg via de server en diens bandbreedte weg.

## Problemen oplossen

- *Het scherm overnemen kan bij Windows-apparaten*: Linux-apparaten hebben in
  plaats daarvan een terminal.
- Een terminal die zegt *De shell kon niet worden gestart*: kijk op Linux of
  root een shell heeft (`getent passwd root`), op Windows of PowerShell er is
  (`%SystemRoot%\System32\WindowsPowerShell\v1.0`). Het agentlog heeft de
  fout.
- Op de machine start `fleetwarden-agent remote-test --screenshot C:\shot.png`
  (als SYSTEM, bijvoorbeeld met `psexec -s`) de helper zoals een sessie dat
  doet en bewaart het eerste beeld.
