# Reports

*Nederlands: [reports.nl.md](reports.nl.md)*

Open **Reports** in the menu for an overview of the whole fleet or of one
group. The dashboard shows the most important numbers as tiles as well.
Everyone who can view devices can open the reports; they only see the
groups they have access to. Scheduled reports and the report settings need
the *Manage report schedules and compliance rules* permission (Administrators by default).

## Compliance

A device is **compliant** when all of these are true:

- it was seen in the last 7 days;
- it has a patch scan;
- it isn't missing a security or critical update that came out more than
  14 days ago (declined updates don't count);
- it has no outdated software;
- every disk of 2 GiB or more has at least 10% free space.

The numbers (7 days, 14 days, 10%) can be changed under
**Reports > Settings**. The Compliance tab shows the percentage per group and
every device that isn't compliant, with the reasons.

## Missing patches

The updates that devices are missing according to their last patch scan,
with the category, the release date and the number of devices, followed by
the devices that need attention. Declined updates are left out. Approve or
install them from **Patch management**.

## Outdated software

FleetWarden compares the programs installed on Windows devices with the
winget catalog (see [software.md](software.md)). A program counts as
outdated when the catalog has a newer version. The match uses the name and
publisher from *Programs and Features*, the way `winget upgrade` does; a
program that isn't in the catalog or can't be matched with certainty is
left out. Store apps are left out as well. Outdated Linux packages show up
under *Missing patches*.

- **Update** opens a deployment of the newest version for the devices
  that have an old one. When the package isn't in the software library yet, it is added
  first (this needs *Manage the software library*).
- **Ignore** leaves a program out of the report and the compliance check,
  for example when you keep an old version on purpose. Undo it under
  **Reports > Settings**.

## Assets

Software licences that are over-used or end soon, and the devices whose
warranty has ended or ends soon. *Ending soon* is set under **Settings**
(90 days by default). The CSV export is the complete asset register. See
[assets.md](assets.md).

## Export

**PDF** and **CSV** at the top of the page export the open tab for the
chosen group, in the language of the interface. The PDF carries the logo,
color and product name from **Branding**. The CSV is UTF-8 and opens
directly in Excel: it uses a semicolon in Dutch and a comma in English.
Every export is written to the audit log.

## Scheduled reports

**Reports > Scheduled reports > New scheduled report** mails one or more reports to
up to 20 addresses:

- daily, weekly (on a chosen day) or monthly (on day 1 to 28), at a chosen
  time in a chosen time zone;
- all groups or a selection;
- as one PDF with all chosen reports and/or a CSV per report;
- in English or Dutch.

The mail itself summarises the most important numbers. It goes through the
mail server under **Administration > Mail server**, which must be turned on.
**Send now** sends a schedule at once without moving the next run; the list
shows when it last ran and the error, if any.

The PDF is made by the server itself (no browser or external service is
needed) with the Inter font, which is included under the SIL Open Font
License (`internal/reports/fonts/OFL.txt`).

## API

| Method | Path | Permission |
| --- | --- | --- |
| GET | `/api/v1/reports/{compliance,patches,software}?groups=1,2` | devices.view |
| GET | `/api/v1/reports/{kind}/export?format=pdf\|csv&groups=&lang=` | devices.view |
| GET, PUT | `/api/v1/reports/settings` | reports.manage (GET: devices.view) |
| POST | `/api/v1/reports/software/ignore` | reports.manage |
| GET, POST | `/api/v1/reports/schedules` | reports.manage |
| PUT, DELETE | `/api/v1/reports/schedules/{id}` | reports.manage |
| POST | `/api/v1/reports/schedules/{id}/send` | reports.manage |
