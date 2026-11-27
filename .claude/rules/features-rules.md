# Feature Rules (PART 18–23)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL — NEVER DO

- Never rely on an external cron service for scheduled work.
- Never send email without configured SMTP or claim delivery when it failed.
- Never disable or weaken backup verification, restore safety, or update checksum validation.
- Never expose secrets or personal data through notifications, metrics, or GeoIP output.

## CRITICAL — ALWAYS DO

- Provide built-in email/notification, scheduler, GeoIP, metrics, backup/restore, and update capabilities.
- Make scheduled work persistent, bounded, observable, and safe to retry.
- Verify backup integrity and test restore paths; keep updates atomic and checksum-verified.
- Keep operational features configurable and manageable through the admin interface.

## Key rules

- Notifications use channel configuration and user preferences; SMTP availability is detected and reported.
- Metrics avoid secret and high-cardinality values.
- Updates never replace binaries before verification and retain a recoverable prior state.

For complete details, see AI.md PART 18–23.
