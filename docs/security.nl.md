# Beveiligingsstatus

*English: [security.md](security.md)*

Open **Beveiliging** in het menu om per apparaat te zien of de schijven
versleuteld zijn, of antivirus aan staat en bijgewerkt is, of de firewall aan
staat, welke dreigingen Microsoft Defender heeft gevonden, of Secure Boot en de
TPM aan staan en wie lokale beheerder is. De tegels bovenaan tellen de
problemen; klik op een tegel om alleen de apparaten met dat probleem te zien.
Elk apparaat heeft ook een tabblad **Beveiliging**.

De agent stuurt zijn beveiligingsstatus na elke inventarisatie (standaard elke
15 minuten, zie **Instellingen > Agentscans**) en wanneer je op het apparaat op
**Nu controleren** klikt. Oudere agents sturen niets tot ze zijn bijgewerkt.

## Wat wordt gelezen

| | Windows | Linux |
|---|---|---|
| Schijfversleuteling | BitLocker per vaste schijf (`Get-BitLockerVolume`): status, percentage, beveiliging, sleutelbeveiligingen | Gekoppelde bestandssystemen en of er LUKS / dm-crypt onder ligt (`lsblk`) |
| Herstelsleutels | BitLocker-herstelwachtwoorden, versleuteld opgeslagen op de server | – |
| Antivirus | Producten die Windows-beveiligingscentrum kent, plus Microsoft Defender (modus, realtime- en manipulatiebeveiliging, definities, laatste scans, detecties van de laatste 30 dagen) | Microsoft Defender for Endpoint (`mdatp`) en ClamAV |
| Firewall | Profielen Domein, Privé en Openbaar (effectief beleid) en het huidige netwerk | Proxmox VE-firewall, ufw, firewalld, of een filterende nftables/iptables-regelset |
| Lokale beheerders | Leden van de groep Administrators (ook accounts die niet meer bestaan) | Leden van sudo, wheel en admin, en accounts met UID 0 |
| Secure Boot, TPM | `Confirm-SecureBootUEFI`, `Win32_Tpm` | EFI-variabele `SecureBoot`, `/sys/class/tpm` |

Zo wordt de samenvatting bepaald:

- **Versleuteld**: de systeemschijf is versleuteld en beveiligd. **Deels
  versleuteld**: de systeemschijf wel, een andere vaste schijf niet (op Linux
  telt naast `/` alleen `/home`). **Onderbroken**: BitLocker-beveiliging is
  gepauzeerd, bijvoorbeeld voor een BIOS-update; de sleutel staat dan leesbaar
  op de schijf.
- **Antivirus OK**: minstens één product staat aan en is bijgewerkt.
  Linux-apparaten zonder antivirus tonen "Geen" in grijs en tellen niet als
  probleem.
- **Firewall deels uit**: minstens één Windows-profiel staat uit.

## BitLocker-herstelsleutels

De agent stuurt de herstelwachtwoorden van elke BitLocker-schijf mee. De server
versleutelt ze met zijn geheime sleutel (dezelfde die de mail- en
SSO-geheimen beschermt, dus die zit in elke back-up) en zet ze nooit in het
opgeslagen rapport. Sleutels blijven vindbaar als ze vervangen zijn of als het
apparaat is verwijderd, zodat een oude afdruk of een opnieuw geïnstalleerde pc
nog op te zoeken is.

Een gebruiker helpen bij het BitLocker-herstelscherm:

1. Open **Beveiliging > Herstelsleutels**.
2. Typ de eerste tekens van de **sleutel-id** die het herstelscherm toont, de
   computernaam of het serienummer.
3. Klik op **Tonen**, typ eventueel een reden (ticketnummer) en lees de 48
   cijfers voor.
4. Klik op **Nieuwe herstelsleutel** zodra de gebruiker weer in Windows is.
   Het apparaat voegt een nieuw herstelwachtwoord toe, verwijdert de oude en
   meldt de nieuwe sleutel meteen. Op apparaten in een domein gaat de nieuwe
   sleutel ook naar Active Directory, op Entra-apparaten naar Entra ID, als dat
   is ingericht.

Het tonen van een sleutel komt met je naam en de reden in het auditlogboek.
Beheerders en technici hebben het recht **security.keys**; alleen-lezen
gebruikers zien de status maar geen sleutels. Gebruikers met een
beperking tot apparaatgroepen zien alleen de sleutels van die groepen;
sleutels van verwijderde apparaten zijn alleen voor gebruikers zonder
beperking.

## Defender-acties

Op Windows-apparaten heeft het tabblad **Beveiliging** de knoppen **Snelle
scan**, **Volledige scan** en **Definities bijwerken** (recht devices.manage).
Een offline apparaat voert de actie uit als het binnen een dag terugkomt. Een
volledige scan kan uren duren; zonder antwoord binnen 13 uur wordt de actie als
mislukt gemarkeerd. Na een actie stuurt het apparaat meteen een nieuwe status.

## Meldingen

Onder **Meldingen > Regels** gebruiken vier soorten regels de
beveiligingsstatus:

- **Dreiging gevonden**: Defender meldt een dreiging die niet is verwijderd of
  in quarantaine gezet. Een regel van deze soort bestaat vanaf het begin.
- **Antivirus uit of verouderd** (alleen Windows).
- **Firewall uit**: een profiel of de Linux-firewall staat uit.
- **Systeemschijf niet versleuteld**: niet, deels of onderbroken. Beperk deze
  tot werkplekken als je servers niet versleuteld zijn.

Meldingen sluiten vanzelf zodra het apparaat meldt dat het probleem weg is.
