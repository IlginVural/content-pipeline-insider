# Content Pipeline  

A rendering capability that lets email templates reference **live data from external sources at render time**, through a secure, config-driven pipeline.

> Instead of embedding raw URLs inside templates — which raises security, secrets, and change-management concerns — templates reference pre-approved **content sources** by name. The platform resolves them through three configurable layers: **upstream**, **transformer**, and **renderer**.

**Status:** Proof of Concept — active development.

---

## Table of contents

- [Motivation](#motivation)
- [How it works](#how-it-works)
- [Architecture](#architecture)
- [Getting started](#getting-started)
- [Project structure](#project-structure)
- [API](#api)
- [Roadmap](#roadmap)
- [Design decisions](#design-decisions)
- [References](#references)

---

## Motivation

In most email platforms, the data inside a message is **baked at send time**. If a product goes out of stock five minutes after the email is sent, the message still says *"only 3 left in stock"*.

A common pattern to solve this is inline URL injection — allowing template authors to write `{{content url="..."}}` tags and having the render engine call that URL live. This works, but has serious drawbacks in any multi-tenant, enterprise-facing context:

- **SSRF risk** — a compromised template author can point at internal metadata endpoints or private intranets
- **Data exfiltration** — arbitrary URLs can post user PII to attacker-controlled servers
- **XSS** — raw responses inject directly into HTML with no schema enforcement
- **Secret sprawl** — auth tokens end up inside templates or force template gymnastics
- **No change control** — upstream API changes silently break every template referencing them

This project explores a **safer, layered alternative**: template authors never touch URLs. An operator pre-registers **named content sources**, each mapped to three composable layers, and templates reference them by name.

---

## How it works

A template contains something like:

​```
{{external_content name="product_stock" params={sku: "12345"}}}
​```

At render time, the platform:

1. Looks up `product_stock` in the **content source registry**
2. Runs the mapped **upstream** — an authenticated, timeout-bounded HTTP call to an external API
3. Passes the raw response through the mapped **transformer** — a declarative mapping to a clean, predictable shape
4. Feeds the clean data into the mapped **renderer** — a template fragment that produces the final HTML
5. Injects the resulting HTML into the message

The template author sees only a name. The URL, auth, timeout, response shape, and rendering rules are all pre-approved configuration.

---

## Architecture

Three cleanly separated layers, each with a single responsibility:

| Layer | Responsibility | Config