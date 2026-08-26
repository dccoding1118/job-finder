# jobfinder

[![CI](https://github.com/dccoding1118/job-finder/actions/workflows/ci.yml/badge.svg)](https://github.com/dccoding1118/job-finder/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/dccoding1118/job-finder?sort=semver)](https://github.com/dccoding1118/job-finder/releases/latest)
[![License](https://img.shields.io/badge/license-AGPL--3.0-blue)](LICENSE)

**jobfinder** is an anonymous AI job-matching tool that runs on your own machine. It takes a **de-identified profile** as its baseline and matches it against public job openings on Taiwan job sites: hard requirements decide whether a job is a fit at all, four weighted dimensions decide whether it is worth applying to, and every verdict comes with the reasoning behind it. Only after you choose to pursue a job is a tailored cover letter written for it, and reviewed before it reaches you. The application itself stays yours: you decide which openings deserve one, you have the final say over what it claims, and it goes out under your name — which is exactly what makes it accurate and worth reading.

A Chrome extension is how you use it, and it also gives you live matching results while you browse Taiwan job sites. Everything runs locally, and no personal contact information ever leaves your machine.

## Why jobfinder

- **Matched on your full set of conditions, not a keyword** — Acceptable locations, years of experience, salary floor, work arrangement and your exclusions are all applied, and the full job description is read against your full profile rather than skimmed for terms.
- **"Is it a fit" and "is it worth applying to" are answered separately** — Missing a hard requirement makes a job unfit; it is not quietly turned into a low score. Only jobs that clear every hard requirement are scored and ranked, so a high score always means something.
- **Missing information never rules a job out** — Every condition is judged honestly, including "the description doesn't say". A job that is not disqualified but lacks information waits on a *to review* list until the full description is available, instead of silently disappearing.
- **Anonymous by design** — Your profile carries no name, contact details, school or employer names, and recruiter contact details found in a job posting are stripped the moment they arrive. Nothing personally identifying is ever sent to a model. Cover letters are signed with the placeholders `[你的姓名]` (your name) and `[你的聯絡方式]` (your contact details), which you fill in yourself.
- **Cover letters written on request, and checked before you read them** — Recommended jobs wait for your decision instead of consuming quota upfront. Each letter is reviewed for invented skills, vague filler and overstated claims, and cross-checked so that every technical term in it actually appears in your profile or in the job description.
- **You control what gets spent and when** — A daily cap, a pause switch for automatic processing, and a "process this one now" action for a single job. Changing your criteria never silently re-runs everything: existing jobs are re-evaluated only when you ask, and letters you already have are never overwritten.

## How It Works

```
 scheduled matching ─┐
 (daily, hands-off)  │
                     ├─► fit check ─► scoring ─► you choose ─► cover letter ─► review ─► your application
 assisted browsing ──┘
 (while you browse)
```

Openings reach you through two channels: **scheduled matching**, which runs on a daily timer without you, and **assisted browsing**, where the extension evaluates whatever you are looking at on a Taiwan job site. Both feed the same evaluation, so an opening you came across while browsing and one the schedule found are never judged differently.

Scoring covers four dimensions: role-content fit (50%), compensation and benefits fit (20%), nice-to-have qualifications fit (15%) and industry fit (15%). Jobs above the threshold are marked as recommended and wait for your decision. Drafting and reviewing a cover letter are two independent steps, so a weak draft is caught rather than handed to you.

### Supported Taiwan job sites

| Site | Mode | What you do |
|---|---|---|
| 104 | Assisted browsing | Browse the site as usual; each listing is marked in place with its verdict, and opening a job shows the complete result |
| Cake | Assisted browsing | Same as above |
| Yourator | Scheduled matching | Nothing — matching runs on a daily schedule and results appear in the side panel |

**Assisted browsing** is the default mode for a newly supported site. You set your own search criteria on the site and browse; jobs already evaluated show their existing verdict and score immediately, and jobs seen for the first time get an instant verdict from your hard requirements while you are still on the page.

**Scheduled matching** runs unattended once a day and needs no interaction.

## Features

- **A verdict you can argue with** — Every condition's conclusion, including exactly which requirement failed, plus the four dimension scores and the recommendation rationale, so you can tell a genuine mismatch from criteria that need adjusting.
- **In-place marking while browsing** — Listings are marked unfit / recommended / not recommended / to review as you browse, and a job page shows the full result.
- **The same job posted twice, handled once** — When an opening appears on several sites, the duplicates are linked so it is scored once and carries one cover letter and one application status. Uncertain pairs are left for you to decide, and any link can be undone.
- **Full-page profile editor** — Edit your profile in a form rather than by hand; it is validated and checked for personal information on every save, and takes effect immediately.
- **Application tracking** — Pending, applied, interview, offer, ghosted, dropped, tracked per job.
- **A to-review list** — Jobs that could not be judged from a listing alone are collected in one place for you to open and complete.

## Prerequisites

| Item | Linux | Windows |
|---|---|---|
| OS | A distribution with a systemd user session | Windows 10 / 11 |
| Shell | bash, with `curl`, `tar`, `unzip`, `sha256sum` | PowerShell 5.1+ (built in) |
| Agent CLI | `claude` or `codex` on PATH | Same |
| Browser | Chrome 114+ | Same |
| Service persistence | `loginctl enable-linger` required, or scheduling stops at logout | Tasks trigger at logon; days without a logon are skipped |

macOS binaries are published, but macOS is not a supported deployment platform.

## Install

The bootstrap script only downloads and verifies checksums; the installation itself is performed by `jobfinder install`, which sets up every location, generates an access token, mounts scheduling and confirms the service is actually running. Your existing configuration and profile are never overwritten. Where an installation is already present, the script hands over to `jobfinder update` instead, which keeps a rollback copy.

**Linux**

```bash
curl -fsSL https://raw.githubusercontent.com/dccoding1118/job-finder/main/scripts/bootstrap/install.sh | bash
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/dccoding1118/job-finder/main/scripts/bootstrap/install.ps1 | iex
```

The script installs the backend by default. It can also fetch the extension — `--extension` / `-Extension` downloads it, verifies it and unpacks it into a per-tag directory ready for Chrome, and `--all` / `-All` does the backend and the extension in one run. That is what a browser machine with no backend of its own uses.

```bash
# Linux; the pipe needs `-s --` before the flag
curl -fsSL https://raw.githubusercontent.com/dccoding1118/job-finder/main/scripts/bootstrap/install.sh | bash -s -- --extension
```

```powershell
# Windows; `iex` cannot take arguments, so save the script first
irm https://raw.githubusercontent.com/dccoding1118/job-finder/main/scripts/bootstrap/install.ps1 -OutFile install.ps1
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Extension
```

Loading the unpacked directory into Chrome stays manual — Chrome has no command-line entry point for it.

<details>
<summary>Or download the artifact and install manually (identical result)</summary>

Download the artifact and `SHA256SUMS` from [Releases](https://github.com/dccoding1118/job-finder/releases/latest), verify, extract, and run the `jobfinder install` inside:

Set `VER` to the tag shown on the Releases page.

```bash
# Linux
REPO=dccoding1118/job-finder
cd "$(mktemp -d)"
curl -fsSLO "https://github.com/$REPO/releases/download/$VER/jobfinder_${VER}_linux_amd64.tar.gz"
curl -fsSLO "https://github.com/$REPO/releases/download/$VER/SHA256SUMS"
sha256sum -c --ignore-missing SHA256SUMS
tar -xzf "jobfinder_${VER}_linux_amd64.tar.gz"
cd "jobfinder_${VER}_linux_amd64" && ./jobfinder install
```

```powershell
# Windows: clear the Mark of the Web after extracting, or SmartScreen will block execution
$name = "jobfinder_${VER}_windows_amd64"
Expand-Archive "$name.zip" -DestinationPath . -Force
Set-Location $name
Get-ChildItem -Recurse | Unblock-File
.\jobfinder.exe install
```

Checksum verification commands and a step-by-step walkthrough are in the getting-started guide, §3.1 and §4.1.

</details>

The extracted executable is only the installer — it places its own copy where it belongs, so the extracted directory can be deleted afterwards. On Windows two executables are installed, one for your own use and one for scheduled runs, so nothing pops up a console window on your desktop; both are kept at the same version.

## Getting Started

`jobfinder paths` prints every location this machine actually uses — start there whenever something is not where you expect it.

### 1. Load the extension

The extension is how you use jobfinder, so it comes before any other setup. It ships as its own release artifact, `jobfinder-extension_<tag>.zip` — the platform archive does not contain it, and the installer does not fetch it. **Keep it at the same version as the backend**: both are published from one tag, and nothing checks the pairing at runtime, so a mismatch misbehaves quietly rather than warning you.

Extract it somewhere permanent (Chrome reads an unpacked extension from that directory on every start), then in Chrome open `chrome://extensions` → developer mode → *Load unpacked* → select that directory. The extension ID is a constant across machines and versions; use the one shown on the card.

### 2. Connect it to the backend

Set `api.extension_origin` in the configuration file to `chrome-extension://<extension ID>` and restart the service. Until this is set, the side panel cannot load anything. The ID never changes, so this is a one-time step.

Then open the extension's options page and enter the endpoint (`http://127.0.0.1:8686` by default) and the `api.token` value from the configuration file.

> On Windows, always specify the encoding when reading or writing the configuration file — PowerShell 5.1 neither writes nor reads UTF-8 by default, and a file corrupted on write fails silently. The exact commands are in the getting-started guide, §5.3.

### 3. Fill in your profile

The installer seeds an anonymised example profile; replace it with your own. The side panel's full-page editor is the way to do it — it validates the structure and scans for personal information on every save.

Editing the `profile` file listed by `jobfinder paths` by hand works too. Either way, check the result:

```bash
jobfinder profile lint
# profile lint passed
```

### 4. Day-to-day

Work from the side panel. Scheduled matching runs daily at 08:30 (Asia/Taipei), and everything else has a command-line equivalent:

| Action | Linux | Windows |
|---|---|---|
| Match now | `systemctl --user start jobfinder-run.service` | `Start-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'run'` |
| Restart the service | `systemctl --user restart jobfinder-api.service` | `Stop-ScheduledTask`, wait for the process to exit, then `Start-ScheduledTask` (guide §9) |
| Read logs | `journalctl --user -u jobfinder-api.service` | The rotating file pointed at by `log.file` |

To update, re-run the bootstrap script or run `jobfinder update` against a newer artifact; `jobfinder rollback` reverts it. Replace the extension in the same pass, so the two stay on one version — `--all` / `-All` covers both halves of that pass.

The backend and the browser run on the same machine. The extension holds `host_permissions` for `http://127.0.0.1/*` and `http://[::1]/*` only, so its endpoint is always a local loopback address. Running the backend elsewhere means forwarding that machine's loopback port to your own, which is yours to arrange and yours to support.

The **full walkthrough** — manual checksum verification, profile setup, extension wiring and troubleshooting — is in the **[getting-started guide](docs/guides/getting-started.md)**.

## Principles

These hold regardless of which features you use, and they are why the tool behaves the way it does above.

| Principle | What it means |
|---|---|
| **Zero PII** | Names, emails, phone numbers, school names and employer names appear nowhere — not in your profile, not in the prompts sent to a model, not in the cover letters. Contact details found in a job posting are stripped on arrival |
| **Human in the loop** | The tool produces recommendations and drafts, nothing more. It never applies on your behalf, never edits your profile, and never alters your application history — every outgoing action is one you took deliberately |
| **Compliance** | Only public, login-free pages, `robots.txt` respected, nothing bypassed; assisted browsing acts solely on pages already open in your own browser |

## Status

| Deployment form | State |
|---|---|
| **Self-hosted — Linux** | Available. Install, update, rollback and extension wiring verified against release artifacts |
| **Self-hosted — Windows** | Implementation and CI coverage in place; on-device deployment acceptance is in progress. Steps are in the getting-started guide, §4 |
| **Managed cloud** | Planned, so that no installation is needed at all. Accounts, billing and multi-tenancy are out of scope for this repository |

| Item | Today | Planned |
|---|---|---|
| Getting the extension | Loaded unpacked in developer mode | Published on the Chrome Web Store, making the connection step unnecessary |
| LLM access | Requires the headless `claude` / `codex` CLI | Usable with your own API key, with no CLI installed |
| Supported job sites | Three (see the table above) | Extended incrementally, with assisted browsing as the default mode |

The full staged plan is in [docs/roadmap.md](docs/roadmap.md).

## Documentation

| Document | Contents |
|---|---|
| [docs/guides/getting-started.md](docs/guides/getting-started.md) | Getting started: install, configure, wire up the extension, daily operation, troubleshooting |
| [docs/PRD.md](docs/PRD.md) | Requirements and scope |
| [docs/roadmap.md](docs/roadmap.md) | Product positioning and staged plan |
| [AGENTS.md](AGENTS.md) | Development guide: architecture, module contracts, testing and deployment (written in Traditional Chinese) |

## License and Contributing

Copyright (c) 2026 Dennis Chan. Licensed under [AGPL-3.0](LICENSE): you are free to use, modify and self-host it; if you offer a modified version as a network service, you must publish your modifications.

**This project does not accept external pull requests.** Issues are open for bug reports and feature suggestions, with no guarantee that they will be acted on. See [CONTRIBUTING.md](CONTRIBUTING.md).
