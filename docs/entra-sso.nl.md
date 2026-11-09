# Inloggen met Microsoft Entra ID

[English](entra-sso.md)

Single sign-on is **optioneel**. Lokale accounts met rollen werken ook zonder,
en het lokale account `admin` blijft altijd beschikbaar als noodaccount. SSO
staat uit tot je het aanzet onder **Administratie > Authenticatie**.

Je hebt nodig: de rol Globale beheerder, Toepassingsbeheerder of
Cloudtoepassingsbeheerder in Entra ID, en de publieke URL van FleetWarden
(bijvoorbeeld `https://fleet.example.com`).

## 1. Controleer de publieke URL

Open in FleetWarden **Administratie > Instellingen** en vul de **Publieke URL**
in (het adres van FleetWarden via je reverse proxy). Open daarna
**Administratie > Authenticatie** en kopieer de **Omleidings-URI**. Die ziet
eruit als `https://fleet.example.com/api/v1/auth/sso/callback`.

## 2. Registreer de applicatie

1. Ga naar <https://entra.microsoft.com> > **Toepassingen > App-registraties > Nieuwe registratie**.
2. Naam: `FleetWarden` (of je eigen productnaam).
3. Ondersteunde accounttypen: **Alleen accounts in deze organisatiemap (één tenant)**.
4. Omleidings-URI: platform **Web**, plak de URI uit stap 1.
5. Klik op **Registreren**.
6. Kopieer op de overzichtspagina de **Applicatie-id (client)** en de **Map-id (tenant)**.

## 3. Maak een clientgeheim

1. **Certificaten en geheimen > Clientgeheimen > Nieuw clientgeheim**.
2. Beschrijving `FleetWarden`, verloopt bijvoorbeeld na 24 maanden. Zet een
   herinnering in je agenda om het op tijd te vernieuwen.
3. Kopieer direct de **Waarde** (niet de geheim-id); die wordt maar één keer getoond.

## 4. Kies hoe rollen worden toegekend

FleetWarden leest de rollen bij **elke** login uit het ID-token. Haal je iemand
in Entra uit de groep of app-rol, dan verliest die bij de volgende login de
toegang (een sessie duurt hooguit 12 uur). Wie nergens bij past, kan niet inloggen.

### Optie A (aangeraden): app-rollen

App-rollen werken ook voor gebruikers die in veel groepen zitten.

1. **App-rollen > App-rol maken**, drie keer:

   | Weergavenaam | Toegestane typen leden | Waarde |
   | --- | --- | --- |
   | FleetWarden Beheerder | Gebruikers/groepen | `FleetWarden.Admin` |
   | FleetWarden Technicus | Gebruikers/groepen | `FleetWarden.Technician` |
   | FleetWarden Alleen lezen | Gebruikers/groepen | `FleetWarden.ReadOnly` |

2. Ga naar **Bedrijfstoepassingen > FleetWarden > Gebruikers en groepen > Gebruiker/groep toevoegen**
   en wijs mensen of groepen aan deze rollen toe.
3. Optioneel: zet onder **Bedrijfstoepassingen > FleetWarden > Eigenschappen**
   **Toewijzing vereist?** op **Ja**, zodat alleen toegewezen gebruikers kunnen inloggen.

### Optie B: beveiligingsgroepen

1. **Tokenconfiguratie > Groepsclaim toevoegen**, kies **Beveiligingsgroepen** en
   kies bij het **ID**-token **Groeps-id**.
2. Kopieer de **Object-id** van elke groep die je wilt gebruiken (**Groepen > Alle groepen**).

Gebruikers in meer dan 200 groepen krijgen geen groepen in het token
(groepsoverschrijding). Gebruik voor hen app-rollen, of kies in de groepsclaim
**Groepen die aan de toepassing zijn toegewezen** en wijs de groepen toe in de
bedrijfstoepassing.

## 5. Stel FleetWarden in

1. **Administratie > Authenticatie**: vul tenant-ID, client-ID en client secret in.
2. Klik op **Test verbinding**. Die controleert of de tenant bestaat en het geheim werkt.
3. Voeg onder **Rolkoppelingen** een regel toe per app-rolwaarde (optie A) of
   groeps-object-id (optie B) en kies de FleetWarden-rol. Past iemand bij
   meerdere regels, dan geldt de rol met de meeste rechten.
4. Zet **Inloggen met Microsoft aanzetten** aan en klik op **Opslaan**.
5. Meld je af. Op de inlogpagina staat nu **Inloggen met Microsoft**; het lokale
   account vind je onder **Inloggen met een lokaal account**.

## Noodtoegang

- Het lokale account `admin` blijft werken als Entra niet bereikbaar of verkeerd ingesteld is.
- Beperk vanaf waar het mag inloggen onder **Nood-beheerder > Toegestane netwerken**
  (bijvoorbeeld `192.168.1.0/24`). FleetWarden weigert een lijst waar je eigen
  adres niet in valt, zodat je jezelf niet per ongeluk buitensluit.
- Wachtwoord of authenticator kwijt? Voer op de server
  `sudo fleetwarden-server reset-admin` uit voor een nieuw eenmalig wachtwoord.

## Problemen oplossen

| Melding op de inlogpagina | Oorzaak |
| --- | --- |
| Je account zit niet in een groep of app-rol met toegang | Geen rolkoppeling past bij de gebruiker; controleer de toewijzing en de koppelingen. |
| Inloggen met Microsoft is mislukt | Verkeerd of verlopen geheim, of een omleidings-URI die niet klopt. Details staan in het auditlog. |
| AADSTS50011 bij Microsoft | De omleidings-URI in de app-registratie wijkt af van die in FleetWarden. Stel de publieke URL in. |
| Het inloggen duurde te lang | De inlogpagina stond langer dan 10 minuten open. Probeer het opnieuw. |

## Mail versturen via Microsoft 365 (optioneel)

De mailserverinstellingen ondersteunen OAuth 2.0 (XOAUTH2) voor Exchange Online.
Daarvoor gebruik je een aparte app-registratie met de toepassingsmachtiging
**Office 365 Exchange Online > SMTP.SendAsApp** (met beheerderstoestemming),
plus een service principal met toegang tot de verzendende mailbox; zie het
Microsoft-artikel "Authenticate an IMAP, POP or SMTP connection using OAuth".
Vul tenant-ID, client-ID en geheim in op de pagina **Mailserver** en gebruik het
mailboxadres als gebruikersnaam. Host `smtp.office365.com`, poort 587, STARTTLS.
