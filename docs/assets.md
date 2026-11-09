# Asset management

*Nederlands: [assets.nl.md](assets.nl.md)*

**Assets** in the menu shows who has which device, where it is and until when
its warranty runs, and on a second tab how many software licences you bought
against how many are in use.

## Devices

Every enrolled device is listed with its hardware (manufacturer, model and
serial number, as the agent reports them) and the details you add:

| Field | Meaning |
|---|---|
| Owner | Name or e-mail address. When nobody is filled in, the list shows the signed-in user in grey; the edit form has a button to copy it. |
| Asset tag | Your own inventory number. |
| Location, department | Free text, for example *Utrecht, 2nd floor* and *Finance*. |
| Supplier, order number | Where and under which order the device was bought. |
| Purchased, warranty until | Dates. The warranty date drives the states below. |

The tiles at the top count the devices whose warranty has **ended**, **ends
soon**, is **valid** or is **unknown**; click a tile to filter the list.
*Ends soon* means within the number of days set under **Reports > Settings >
Ending soon** (90 by default). Search finds host names, serial numbers,
models, owners, locations, asset tags and suppliers.

Edit one device with the pencil, or on the device page in the **Asset
details** card. To change many at once, tick the devices and choose **Edit
asset details**: only the fields you tick change.

### CSV import and export

**Export CSV** downloads the register. **Import CSV** reads a file, for
example the asset list of your supplier:

- Devices are found by serial number first and then by host name (the short
  name matches `pc-01.corp.example` too). Rows that match no device, or
  several, are listed and skipped.
- Recognised headings, in English or Dutch: host name, serial number, asset
  tag, owner, location, department, supplier, order number, purchase date and
  warranty end (also *Service Tag*, *Serienummer*, *Garantie tot*,
  *Eigenaar*, *Locatie*, *Leverancier*, ...). Other columns are ignored.
- Empty cells keep the current value; a single dash (`-`) clears it.
- Dates as `YYYY-MM-DD`, or day first as `DD-MM-YYYY`, `DD/MM/YYYY` or
  `DD.MM.YYYY`.
- Commas, semicolons and tabs all work as separators.

The import first shows what it would change; nothing is saved until you
confirm. An exported file imports again without changes.

## Software licences

A licence has a name, the number of **seats** you bought, an optional end
date (empty is perpetual), cost, notes and an optional licence key.
**Program name** says which installed programs use a seat, as they appear
under *Software* on a device; `*` is a wildcard, for example
`Microsoft 365 Apps*`. An optional **publisher** narrows it down. While you
type, the form shows on how many devices the match is found and which program
names it counts.

**In use** is the number of devices with a matching program, across all
devices. A licence is **over-used** when more devices have the program than
there are seats, and **ending soon** within the same number of days as
warranties. The eye icon lists the devices that use the licence; users who
see only some groups see only those devices.

Licence keys are stored encrypted with the server's secret key. Only users
with the *Manage assets and software licences* permission can show a key, and
every time a key is shown it is recorded in the audit log.

## Alerts and reports

- **Alerts**: new installations have the rule *Warranty ending* (60 days
  before the end, no recipients). Add recipients under **Alerts > Alert
  rules**, or make your own rule of the kind *The warranty ends*. An alert
  stays open while the warranty has ended, and closes when a later date is
  filled in. See [alerts.md](alerts.md).
- **Reports > Assets** shows over-used and ending licences and the devices
  whose warranty ended or ends soon. As PDF it has the same tables; the CSV
  is the complete asset register. It can be mailed on a schedule like the
  other reports, see [reports.md](reports.md).

## Permissions

Everyone who sees devices sees the asset register and the licences.
Administrators and technicians may change them (permission
`assets.manage`); read-only users may not.

## Not included

Warranty dates are not fetched from Dell, HP or Lenovo automatically: their
warranty services need a separate API agreement per organisation. Import the
asset list your supplier sends instead.
