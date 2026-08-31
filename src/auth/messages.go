package auth

// Messages holds every operator-visible string the server-rendered auth pages emit
// through a redirect. Pages never echo a caller-supplied string as a flash: a redirect
// carries a key, and only a key found in this map is rendered. That keeps the flash
// channel free of injected content and gives src/i18n a single table to translate.
//
// Keys are dotted and stable. Adding a translation means providing the same key set in
// another locale; a missing key falls back to the English text here.
var Messages = map[string]string{
	"auth.registered":              "Your account was created.",
	"auth.registered.verify":       "Your account was created. Check your email for the verification link.",
	"auth.registered.approval":     "Your account was created and is waiting for an administrator to approve it.",
	"auth.signed_in":               "You are signed in.",
	"auth.signed_out":              "You have been signed out.",
	"auth.password.changed":        "Your password was changed and every other session was signed out.",
	"auth.password.reset_sent":     "If an account uses that address, a reset link is on its way.",
	"auth.password.reset_done":     "Your password was reset. Sign in with your new password.",
	"auth.password.mismatch":       "The two passwords did not match.",
	"auth.email.verified":          "Your email address is verified.",
	"auth.email.verification_sent": "Verification email sent.",
	"auth.profile.saved":           "Your profile was saved.",
	"auth.totp.started":            "Scan the secret in your authenticator app, then enter the code to finish.",
	"auth.totp.enabled":            "Two-factor authentication is now on.",
	"auth.totp.disabled":           "Two-factor authentication is now off.",
	"auth.session.revoked":         "That session was signed out.",
	"auth.session.revoked_all":     "Every other session was signed out.",
	"auth.token.revoked":           "That token was revoked and stops working immediately.",
	"auth.account.deleted":         "Your account was closed.",

	"org.created":          "The organization was created.",
	"org.saved":            "The organization settings were saved.",
	"org.deleted":          "The organization was deleted.",
	"org.member.added":     "The member was added.",
	"org.member.role_set":  "The member role was updated.",
	"org.member.removed":   "The member was removed.",
	"org.member.left":      "You left the organization.",
	"org.invite.sent":      "The invitation was created.",
	"org.invite.revoked":   "The invitation was revoked.",
	"org.invite.accepted":  "You joined the organization.",
	"org.transferred":      "Ownership was transferred.",
	"org.confirm_mismatch": "The confirmation text did not match the organization handle.",

	"domain.added":    "The domain was added. Publish the TXT record, then check ownership.",
	"domain.verified": "Ownership is confirmed. A certificate will be requested shortly.",
	"domain.pending":  "The TXT record was not found yet. DNS changes can take up to an hour.",
	"domain.removed":  "The domain was removed.",

	"admin.bootstrapped":     "The administrator account was created.",
	"admin.password.changed": "The administrator password was changed.",
	"admin.totp.enabled":     "Two-factor authentication is now on for this administrator.",
	"admin.totp.disabled":    "Two-factor authentication is now off for this administrator.",
}

// message resolves a flash key. An unknown key yields an empty string, so a crafted
// redirect can never place arbitrary text on a page.
func message(key string) string {
	if key == "" {
		return ""
	}
	return Messages[key]
}
