# Security Policy

supatype-server is maintained by [Supatype](https://github.com/supatype). Below
is our security policy.

## Reporting a vulnerability

Report vulnerabilities privately through GitHub:
[Report a vulnerability](https://github.com/supatype/server/security/advisories/new).

This opens a private advisory visible only to you and the maintainers. Please do
not open a public issue for a security report.

We consider the security of our systems a top priority. No matter how much
effort goes into system security, vulnerabilities can still be present, and we
would like to know about them so we can address them quickly.

## Out of scope

- Clickjacking on pages with no sensitive actions.
- Unauthenticated/logout/login CSRF.
- Attacks requiring MITM or physical access to a user's device.
- Any activity that could lead to the disruption of our service (DoS).
- Content spoofing and text injection issues without showing an attack
  vector/without being able to modify HTML/CSS.
- Email spoofing.
- Missing DNSSEC, CAA, CSP headers.
- Lack of Secure or HTTP only flag on non-sensitive cookies.
- Deadlinks.

## Please do

- Report through the private advisory link above.
- Do not run automated scanners on our infrastructure. If you wish to do this,
  contact us and we will set up a sandbox for you.
- Do not take advantage of the vulnerability, for example by downloading more
  data than necessary to demonstrate it, or deleting or modifying other
  people's data.
- Do not reveal the problem to others until it has been resolved.
- Do not use attacks on physical security, social engineering, distributed
  denial of service, spam or applications of third parties.
- Do provide sufficient information to reproduce the problem. Usually the URL of
  the affected system and a description of the vulnerability is sufficient, but
  complex vulnerabilities may require further explanation.

## What we promise

- We will respond to your report within 3 business days with our evaluation and
  an expected resolution date.
- If you have followed the instructions above, we will not take any legal action
  against you in regard to the report.
- We will handle your report with strict confidentiality, and not pass on your
  personal details to third parties without your permission.
- We will keep you informed of the progress towards resolving the problem.
- In the public information concerning the problem reported, we will credit you
  as the discoverer of the problem, unless you desire otherwise.

We strive to resolve all problems as quickly as possible, and would like to play
an active role in the ultimate publication on the problem after it is resolved.
