---
title: Lebanese Legal Handwriting
sdk: static
app_file: index.html
license: mit
short_description: Read court scans and download a local transcription draft.
---

# Lebanese Legal Handwriting

Inspect three public court scan previews, write a transcription and download a JSON draft. Nothing is submitted. Drafts are unreviewed and have no agreed annotation licence.

Open with a local static server:

```sh
python3 -m http.server 8000 --directory .
```

Then visit `http://localhost:8000`. No install, build or API key is needed. JavaScript modules require HTTP rather than opening the HTML as a file.

Run the state and export checks with Node.js 18 or newer:

```sh
node --test test.mjs
```

The page uses only local scripts, styles and images. It makes no application network requests and has no submission endpoint or analytics. External links open public project resources. Local storage keeps separate drafts by ruling and physical PDF page. If storage is unavailable, editing and export still work in memory and the page reports the limitation. Browser storage is not a backup. Downloads require the visitor's action.

Exports retain the original PDF SHA-256, ruling ID, one-based physical PDF page, exact draft text and self-reported origin. They set `license: null`, `submitted: false`, and `reviewed: false`. A machine-assisted self-report is not proof of human review. No annotation reuse rights are inferred.

## Source and rights

The three unchanged preview images and document hashes are from the public [Lebanese Court Scans dataset](https://huggingface.co/datasets/taktekhq/lebanese-court-scans). They depict rulings 59270 (page 7), 59181 (page 3), and 61180 (page 2). These are mixed court records, not anonymised handwriting samples or reference transcriptions.

Source: [Centre for Research and Studies in Legal Informatics, Lebanese University](https://legallaw.ul.edu.lb/). This independent demo has no institutional endorsement. The MIT licence covers demo code only. It does not relicense the scans. Read the dataset's [RIGHTS.md](https://huggingface.co/datasets/taktekhq/lebanese-court-scans/blob/main/RIGHTS.md) and [CONTRIBUTING.md](https://huggingface.co/datasets/taktekhq/lebanese-court-scans/blob/main/CONTRIBUTING.md) before reuse or sharing. Public availability does not establish individual consent. Avoid sharing sensitive details unnecessarily.

Related: [dataset collection](https://huggingface.co/collections/taktekhq/lebanese-legislation), [Lebanese Legislation](https://lebaneselegislation.com), [project source](https://github.com/taktekhq/lebanese-legislation).
