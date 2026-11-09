# Sign-in with Microsoft Entra ID

[Nederlands](entra-sso.nl.md)

Single sign-on is **optional**. Local accounts with roles work without it, and
the local `admin` account always stays available as an emergency account.
SSO is off until you turn it on under **Administration > Authentication**.

You need: a Global Administrator, Application Administrator or Cloud
Application Administrator role in Entra ID, and the public URL of FleetWarden
(for example `https://fleet.example.com`).

## 1. Check the public URL

In FleetWarden, open **Administration > Settings** and fill in the
**Public URL** (the address of FleetWarden through your reverse proxy). Then
open **Administration > Authentication** and copy the **Redirect URI**. It looks
like `https://fleet.example.com/api/v1/auth/sso/callback`.

## 2. Register the application

1. Go to <https://entra.microsoft.com> > **Applications > App registrations > New registration**.
2. Name: `FleetWarden` (or your product name).
3. Supported account types: **Accounts in this organizational directory only (Single tenant)**.
4. Redirect URI: platform **Web**, paste the redirect URI from step 1.
5. Click **Register**.
6. On the overview page, copy the **Application (client) ID** and the **Directory (tenant) ID**.

## 3. Create a client secret

1. **Certificates & secrets > Client secrets > New client secret**.
2. Description `FleetWarden`, expiry e.g. 24 months. Put a reminder in your
   calendar to renew it before it expires.
3. Copy the **Value** right away (not the Secret ID); it is shown only once.

## 4. Choose how roles are assigned

FleetWarden reads roles from the ID token at **every** sign-in. Remove someone
from the group or app role in Entra and they lose access at their next sign-in
(sessions last at most 12 hours). A user who matches nothing cannot sign in.

### Option A (recommended): app roles

App roles also work for users in many groups.

1. **App roles > Create app role**, three times:

   | Display name | Allowed member types | Value |
   | --- | --- | --- |
   | FleetWarden Administrator | Users/Groups | `FleetWarden.Admin` |
   | FleetWarden Technician | Users/Groups | `FleetWarden.Technician` |
   | FleetWarden Read only | Users/Groups | `FleetWarden.ReadOnly` |

2. Go to **Enterprise applications > FleetWarden > Users and groups > Add user/group**
   and assign people or groups to these roles.
3. Optional: under **Enterprise applications > FleetWarden > Properties**, set
   **Assignment required?** to **Yes** so only assigned users can sign in.

### Option B: security groups

1. **Token configuration > Add groups claim**, choose **Security groups**, and for
   the **ID** token pick **Group ID**.
2. Copy the **Object ID** of each group you want to use (**Groups > All groups**).

Users in more than 200 groups get no groups in the token (group overage).
Use app roles for them, or choose **Groups assigned to the application** in the
groups claim and assign the groups in the enterprise application.

## 5. Configure FleetWarden

1. **Administration > Authentication**: fill in the tenant ID, client ID and client secret.
2. Click **Test connection**. It checks that the tenant exists and that the secret works.
3. Under **Role mappings**, add one line per app role value (option A) or group
   object ID (option B), and pick the FleetWarden role. When someone matches
   several lines, the most privileged role wins.
4. Turn on **Enable sign-in with Microsoft** and click **Save**.
5. Sign out. The sign-in page now shows **Sign in with Microsoft**; the local
   account is under **Sign in with a local account**.

## Emergency access

- The local `admin` account keeps working when Entra is down or misconfigured.
- Limit where it may sign in from under **Emergency administrator > Allowed networks**
  (for example `192.168.1.0/24`). FleetWarden refuses a list that does not include
  your own address, so you cannot lock yourself out by accident.
- Lost the password or authenticator? On the server run
  `sudo fleetwarden-server reset-admin` for a new one-time password.

## Troubleshooting

| Message on the sign-in page | Cause |
| --- | --- |
| Your account is not in any group or app role that has access | No role mapping matches the user; check the assignment and the mappings. |
| Microsoft sign-in failed | Wrong secret, expired secret or a redirect URI that does not match. Check the audit log for details. |
| AADSTS50011 at Microsoft | The redirect URI in the app registration differs from the one shown in FleetWarden. Set the public URL. |
| The sign-in took too long | The sign-in page was open for more than 10 minutes. Try again. |

## Sending mail through Microsoft 365 (optional)

The mail server settings support OAuth 2.0 (XOAUTH2) for Exchange Online. That
uses a separate app registration with the **Office 365 Exchange Online >
SMTP.SendAsApp** application permission (admin consent), plus a service
principal with access to the sending mailbox; see Microsoft's article
"Authenticate an IMAP, POP or SMTP connection using OAuth". Enter its tenant ID,
client ID and secret on the **Mail server** page and use the mailbox address as
username. Host `smtp.office365.com`, port 587, STARTTLS.
