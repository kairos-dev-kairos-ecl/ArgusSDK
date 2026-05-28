# Argus Platform Evolution — Research Report
## Connector Layer, Platform Bridge Architecture & ArgusSDK/ArgusXDR Split

**Classification:** Internal Research  
**Scope:** Architecture viability, tech stack research, effort assessment  
**Constraint:** No code. No plans. No execution. Research and approach only.  
**Output:** Independent evaluation reference

---

## 1. Current State Assessment

### What Argus Has Today

**Core Platform (Phases 1–3 Complete)**

The existing Argus platform is a production-grade LLM observability and detection system built Go-first. The foundational layers are complete and performant:

- **Signal Ingest Pipeline** — ArgusSDK acts as a lightweight telemetry agent embedded in LLM applications. It collects signals and feeds the central Argus instance.
- **10-Layer Signal Taxonomy** — A structured classification schema covering the breadth of LLM behavioral signals (prompt, completion, tool use, latency, token patterns, semantic drift, identity, etc.)
- **Storage Tier** — ClickHouse for high-throughput signal storage and time-series queries. PostgreSQL for configuration and stateful metadata. Redis for ephemeral/hot-path state.
- **Four-Tier Detection Engine** — Deterministic, statistical, temporal, semantic layers. Phases 1–3 complete; detection alerting and frontend remain in progress.
- **Operational Resilience** — Backpressure with priority-based load shedding. Circuit breakers on all outbound calls. Google SRE four golden signals for self-monitoring.
- **Frontend** — React/TypeScript. GUI-first operator experience, no terminal dependency as a design principle.

**What the Pipeline Looks Like Today**

```
LLM Application
    └── ArgusSDK (embedded agent)
            └── Signal Stream (structured, taxonomy-normalized)
                    └── Argus Ingest Layer
                            ├── ClickHouse (signals)
                            ├── PostgreSQL (config/state)
                            └── Redis (ephemeral)
                                    └── Detection Engine → Alerts → Frontend
```

**The Gap This Report Addresses**

Signal streams exist and are structured. They currently flow exclusively into ArgusXDR's internal pipeline. There is no mechanism to route these streams to external platforms. The connector layer is the missing segment.

---

## 2. The Architectural Split — ArgusSDK vs ArgusXDR

### Proposed Model

**ArgusSDK** — Standalone telemetry and signal collection agent. Embedded in LLM applications. Runs headless if required. Its responsibility is collection, normalization against the 10-layer taxonomy, and emission of structured signal streams. It should have no awareness of or dependency on what consumes those streams. This is the distribution unit — open-source friendly, low adoption friction, embeds in any Go or SDK-compatible environment.

**ArgusXDR** — The full detection, alerting, and response platform. The ClickHouse/PostgreSQL/Redis stack, the four-tier detection engine, the frontend. This is the platform product — for organizations without a SIEM investment, organizations that want a dedicated LLM security plane, or organizations that want both (ArgusXDR as primary + connectors feeding their existing platforms as secondary).

**Connector Layer** — Sits between ArgusSDK's signal streams and any external platform. Can attach at two points:
- Directly off ArgusSDK raw signal streams (raw, pre-detection data)
- Off ArgusXDR's processed signal and alert outputs (enriched, post-detection data)

Organizations with a mature SOC will likely want post-detection enriched data. Organizations building their own detection on top of their SIEM will want raw signal streams.

```
LLM Application
    └── ArgusSDK
            └── Signal Stream
                    ├── [Option A] ArgusXDR (full platform)
                    │       └── Processed Signals / Alerts
                    │               └── Connector Layer ──► External Platforms
                    │
                    └── [Option B] Connector Layer (direct, no ArgusXDR)
                                    └── External Platforms
```

This model means ArgusSDK can be adopted independently of ArgusXDR. A Splunk-heavy enterprise can embed ArgusSDK and route directly to Splunk without ever running ArgusXDR. The platform split is a genuine architectural decoupling, not a rebrand.

---

## 3. The Connector Layer — Architecture Research

### Design Philosophy

The connector layer should follow the same principles Argus already applies — interface-driven, circuit-breakered, backpressured — and add one new constraint: every connector is a pluggable unit that implements a common interface. The framework is the constant; connectors are swappable. No connector is coupled to another. Connector failure does not affect the main signal pipeline.

This is the same model OpenTelemetry Collector uses (receiver → processor → exporter pipeline), the same model Fluent Bit uses, and the same model Elastic Beats uses. It is proven at scale.

### The Connector Interface (Conceptual, Not Code)

Each connector must satisfy:
- Accept normalized signal batches
- Authenticate with the target platform
- Transform signals to the target schema
- Deliver with retry and backoff
- Report health and delivery status back to the framework
- Be independently configurable without restarting the rest of the pipeline

### Delivery Guarantees

At-least-once delivery is the practical target. Exactly-once is not achievable across heterogeneous external platforms without platform-specific transaction support that most SIEM endpoints don't offer. The framework should maintain a per-connector acknowledgment state — if a batch is not confirmed, it is retried. A dead-letter mechanism (local buffer, ClickHouse sidecar table, or Redis queue) handles persistent failures without blocking the primary pipeline.

### Connector Configuration Model

Config-driven, not code-driven. Operators should be able to enable, disable, or reconfigure connectors without touching application code. YAML or TOML config per connector, loaded at runtime, specifying: target endpoint, authentication method, schema mapping profile, batch size, retry policy, TLS parameters. This is table stakes for enterprise deployment.

---

## 4. The Schema Problem — The Real Engineering Challenge

Connectors are not the hard part. Schema mapping is.

Argus's 10-layer signal taxonomy is rich and LLM-specific. External platforms expect their own normalized schemas. Getting a prompt injection detection event into Sentinel with the correct severity, category, entity relationships, source, and evidence fields — correctly, not approximately — requires careful per-platform mapping.

There are two approaches to this:

### Approach A — Direct Per-Platform Mapping

Map from Argus taxonomy directly to each target schema (ECS for Elastic, UDM for Chronicle, ASFF for AWS, CEF for ArcSight, etc.). Maximum fidelity per platform. High total mapping effort as number of connectors grows. Every new connector requires a new mapping. This is the naive approach.

### Approach B — OCSF as Canonical Intermediate (Recommended)

Map Argus taxonomy → OCSF once. Then map OCSF → target platform schema per connector. When a new connector is added, only the OCSF→target mapping is written, not a fresh Argus→target mapping from scratch.

**OCSF (Open Cybersecurity Schema Framework)** is the correct long-term choice here. It was initiated by AWS and Splunk, adopted by CrowdStrike, Microsoft, IBM, Palo Alto Networks, and many others. It is designed to be the universal normalization layer for security telemetry. Version 1.x is stable and in production use. It supports LLM-relevant event classes (application activity, API activity, network activity, process activity) and has extensibility for custom classes.

The implication: the main effort is building a high-quality Argus-to-OCSF mapper once. After that, each connector only needs an OCSF-to-target mapper, which is significantly smaller scope.

### Key Schema Standards Reference

| Standard | Owner | Primary Use | Status |
|----------|-------|-------------|--------|
| OCSF | AWS, Splunk (open) | Universal security schema | Active, v1.x stable |
| ECS (Elastic Common Schema) | Elastic (open) | Elastic/OpenSearch platforms | Active |
| UDM (Unified Data Model) | Google | Chronicle/SecOps | Active, proprietary |
| ASFF | AWS | Security Hub findings | Active, strict schema |
| CEF (Common Event Format) | Micro Focus/ArcSight | Legacy SIEM | Aging, still deployed |
| LEEF (Log Event Extended Format) | IBM | QRadar | Legacy |
| STIX/TAXII | OASIS | Threat intelligence sharing | Active, different use case |

OCSF reduces the schema problem from O(connectors × platforms) to O(connectors) + O(platforms). The reduction compounds as connector count grows.

---

## 5. Target Platform Research

### 5.1 Apache Kafka / Redpanda

**What it is:** Not a SIEM — a distributed event streaming broker. The universal backbone play. If Argus produces to Kafka topics, it is compatible with every downstream platform that has a Kafka consumer, which is most of them.

**Why it matters:** Many enterprise data pipelines already have Kafka. Producing to Kafka means ArgusSDK signals become available to any team's tooling without Argus needing to know what that tooling is.

**Redpanda vs Kafka:** Redpanda is API-compatible with Kafka (speaks the Kafka wire protocol) but is written in C++ with no JVM dependency. Significantly simpler to operate. No ZooKeeper. No per-partition overhead. For organizations that want Kafka semantics without Kafka operational complexity, Redpanda is the better choice. Kafka proper makes sense only where an existing Kafka cluster exists.

**Security:** SASL/SCRAM or SASL/GSSAPI (Kerberos) for authentication. TLS for transport. mTLS supported for mutual authentication. ACLs per topic. Schema Registry available for Avro/Protobuf schema enforcement if needed.

**Connector complexity:** Medium. Kafka producer client in Go is mature (franz-go is the best Go Kafka client — no CGO, pure Go, production-grade). TLS setup is straightforward. The main decision is schema format: JSON is simplest, Protobuf is more efficient, Avro requires a Schema Registry dependency. JSON with OCSF structure is the right starting point.

**Lock-in risk:** Low. Kafka is an open standard. Redpanda is compatible. Multiple client libraries. No vendor lock.

---

### 5.2 Splunk (HEC)

**What it is:** The HTTP Event Collector is Splunk's purpose-built ingest endpoint for structured data. REST over TLS. Token-based authentication. Supports batching, acknowledgment, and indexer clustering transparency.

**Splunk TA (Technology Add-on):** Splunk has a formal partner ecosystem. A Splunk TA is a packaged integration that gets listed on Splunkbase (Splunk's marketplace). This is a distribution channel — a TA-certified connector gets visibility to Splunk's entire install base. The TA model is worth pursuing after the connector itself is stable. It involves submitting to Splunk's certification process, which is documented and achievable.

**Schema:** Splunk ingests raw events by default but expects CIM (Common Information Model) field mapping for use with Splunk's correlation searches and dashboards. Splunk CIM is Splunk's own normalization framework. Mapping Argus signals to CIM fields (specifically the Authentication, Endpoint, and Threat Intelligence data models) is what makes the connector genuinely useful vs. just dumping raw JSON. The OCSF→CIM mapping is a known problem with community prior art.

**Security:** HEC tokens are bearer tokens. TLS 1.2+ required. Token rotation is manual unless managed by the Splunk admin or a secrets manager. Acknowledgment API (indexer acknowledgment) provides delivery guarantees — the connector can confirm that Splunk has indexed the event, not just received it.

**Connector complexity:** Low. HEC is one of the simplest enterprise ingest APIs. Well-documented, stable API surface, extensive community tooling.

**Lock-in risk:** Medium. Splunk CIM mapping is Splunk-specific. The OCSF intermediate helps because OCSF→CIM is a bounded mapping problem, not an Argus-specific one.

---

### 5.3 Microsoft Sentinel

**What it is:** Cloud-native SIEM built on Azure Monitor and Log Analytics. Heavy deployment in regulated industries and Microsoft-centric enterprises.

**Ingest options — two paths exist:**

*Legacy: Log Analytics HTTP Data Collector API.* Simple REST POST. Still works, still documented. Not recommended for new connectors because Microsoft has signaled deprecation intent. Worth knowing about for compatibility, not the right build target.

*Modern: Data Collection Rules (DCR) + Data Collection Endpoints (DCE).* This is Microsoft's current architecture. A DCE is an HTTPS endpoint that accepts data. A DCR defines the transformation and routing of that data inside Azure Monitor. The connector POSTs to the DCE endpoint with an Azure AD bearer token (managed identity or service principal). Microsoft transforms via the DCR and lands the data in a custom Log Analytics table.

The DCR/DCE model is more complex to set up (requires Azure AD app registration or managed identity, DCR definition, DCE provisioning) but is the production-correct path. The complexity is in initial setup, not in the connector code itself.

**Sentinel Content Hub:** Sentinel has a marketplace (Content Hub) for integrations, similar to Splunkbase. An official Sentinel data connector listed in Content Hub is a legitimate enterprise distribution channel. Microsoft requires connectors to follow their Data Connector schema specification, which is documented.

**Security:** Azure AD OAuth 2.0 for authentication. TLS enforced. Service principal with minimum required permissions (Monitoring Metrics Publisher role). Client secret or certificate-based auth — certificate is more secure for production.

**Connector complexity:** Medium. The authentication layer (Azure AD token acquisition, refresh) adds complexity vs. simple token auth platforms. The DCR/DCE setup is infrastructure-level, not connector-code-level.

**Lock-in risk:** High at the Azure layer. Sentinel data stays in Log Analytics workspaces. The connector itself is not locked in, but the target is Azure-specific. Organizations choosing Sentinel are already Azure-committed.

---

### 5.4 CrowdStrike LogScale (formerly Humio)

**What it is:** CrowdStrike's log management and SIEM platform. Separate from the Falcon EDR/XDR surface. High-throughput ingest, fast query, compressed retention. Growing deployment as organizations consolidate onto the CrowdStrike platform stack.

**Ingest:** Structured data ingest API over HTTPS. Token-based authentication (Ingest Token per repository). JSON payload. Supports batching. LogScale also supports HEC-compatible endpoints (Splunk HEC format) which means a Splunk HEC connector could theoretically target LogScale with minimal changes — worth noting for connector reuse.

**LogScale Query Language (LQL):** LogScale has its own query language, distinct from SPL (Splunk) or KQL (Sentinel). Argus signals ingested into LogScale are queryable natively. CrowdStrike also has a parser ecosystem — custom parsers that normalize incoming data can be packaged and shared.

**Integration with Falcon:** LogScale is increasingly used as the data backbone for Falcon's own telemetry. There is potential here beyond just a SIEM connector — Argus signals in LogScale can correlate with Falcon endpoint events, creating cross-domain detection (LLM application behavior correlated with endpoint process activity).

**Security:** HTTPS enforced. Ingest Tokens are repository-scoped. Token rotation via LogScale's management API. No mTLS on the ingest API.

**Connector complexity:** Low. One of the simpler enterprise APIs to target. HEC compatibility option reduces new code.

**Lock-in risk:** Medium. LogScale is CrowdStrike proprietary. The Falcon correlation opportunity is a lock-in accelerant — it increases value but increases dependency.

---

### 5.5 Google Chronicle / SecOps

**What it is:** Google's cloud-native SIEM and security operations platform. Distinct from Google Cloud's general observability. High-throughput, petabyte-scale log storage with a 12-month hot retention default. Increasingly deployed as Google expands its security portfolio.

**Ingest options:**

*Chronicle Ingestion API:* REST API for pushing raw logs and UDM events. Supports structured UDM (Unified Data Model) events directly, or raw logs with a parser applied server-side.

*UDM (Unified Data Model):* Chronicle's normalized event schema. Well-structured, entity-centric (users, devices, processes, network connections). Mapping Argus signals to UDM is the meaningful work here. UDM has event types relevant to LLM observability — `NETWORK_HTTP`, `USER_RESOURCE_ACCESS`, `GENERIC_EVENT` — and supports custom fields via UDM extensions.

*YARA-L Detection:* This is Chronicle's killer feature for this use case. YARA-L is Chronicle's detection rule language, designed for behavioral detection over time (not just per-event matching). Argus signals in Chronicle, correctly mapped to UDM, means security engineers can write YARA-L rules against LLM behavioral patterns — sustained prompt injection attempts across sessions, token exfiltration patterns over time, anomalous tool invocation sequences. This is genuinely powerful and not available on any other platform at this fidelity.

**Authentication:** Google Cloud service account with appropriate IAM roles (Chronicle API User). OAuth 2.0 with service account key or Workload Identity Federation (preferred — no key file required).

**Connector complexity:** Medium-high. The UDM mapping is the most complex schema work of any connector in this list because UDM is entity-centric (events must reference entities — devices, users, processes) and Argus signals are application-centric (LLM app, session, prompt). The mapping requires defining how Argus entities (LLM application instance, session, user identity) translate to UDM entities. Once the mapping is defined, the connector itself is straightforward.

**Lock-in risk:** High at the infrastructure layer (GCP). The Chronicle API is proprietary. The YARA-L detection value is Chronicle-exclusive. Organizations on Chronicle are typically GCP-committed.

---

### 5.6 AWS Security Hub

**What it is:** AWS's centralized security findings aggregator. Collects findings from GuardDuty, Inspector, Macie, third-party integrations, and custom sources. Not a full SIEM — more of a findings hub that normalizes and correlates security posture findings. Integrates with AWS Security Lake for longer-term storage and cross-account analysis.

**ASFF (Amazon Security Finding Format):** The strict normalized schema all Security Hub findings must conform to. Well-documented. Argus detection outputs (not raw signals — ASFF is finding-oriented, not telemetry-oriented) map cleanly to ASFF findings with defined severity, title, description, resources, and remediation fields.

**Custom Findings Provider:** Third parties can register as a custom findings provider and push ASFF findings directly. This is the integration path. AWS charges per finding ingested beyond the free tier.

**AWS Security Lake:** A newer AWS service that stores security data in OCSF format in S3, queryable via Athena. This is highly relevant — Security Lake natively speaks OCSF, which means if Argus produces OCSF-normalized signals, they can be ingested into Security Lake with minimal transformation. Security Lake also has a subscriber model where SIEM tools (Splunk, Sentinel, others) can consume from it. This creates an interesting routing topology: ArgusSDK → OCSF → AWS Security Lake → existing SIEM.

**Security:** AWS Signature Version 4 (SigV4) signing for API calls. IAM role with minimum required permissions (securityhub:BatchImportFindings). No static credentials — assume role or instance profile preferred.

**Connector complexity:** Medium. SigV4 signing adds complexity vs. token auth. ASFF schema is strict (required fields, enum values). The Security Lake angle (OCSF to S3) is actually simpler and more strategically aligned.

**Lock-in risk:** High at the AWS layer. ASFF is proprietary. Security Lake's OCSF storage model is more portable.

---

### 5.7 Elastic / OpenSearch (SIEM-flavored deployments)

**What it is:** Both Elastic SIEM (built on Elasticsearch) and OpenSearch (the AWS-maintained open-source fork) are relevant here as a connector target because a significant number of mid-market and enterprise SIEMs are built on one of these as their storage and query backend. Targeting Elastic's bulk indexing API covers both.

**ECS (Elastic Common Schema):** Elastic's normalization schema. Well-designed, extensible, good community adoption. The `event.category`, `event.type`, `event.outcome` fields provide the primary classification. Custom fields via ECS extensions are supported. Argus signals map reasonably well — LLM applications as `event.category: application`, detections as `event.kind: alert`.

**Detection Rules:** Elastic SIEM has a detection engine with KQL-based rules and machine learning jobs. Argus signals correctly mapped to ECS can trigger existing Elastic detection rules without additional configuration — or custom rules can be written specifically for LLM behavioral patterns.

**Ingest:** Bulk indexing API (`_bulk` endpoint). Standard REST. API key or basic auth. TLS enforced in managed deployments (Elastic Cloud). Self-managed clusters may have TLS optional — the connector should enforce TLS on the client side regardless.

**Fleet/Beats compatibility:** For organizations running Elastic Agent or Beats, there is an option to emit Argus signals in Beats format, making them appear as a standard Beats data source. This enables use of existing Kibana dashboards and Fleet management without additional integration work. This is optional optimization, not required for the connector.

**Connector complexity:** Low. The Bulk API is one of the most widely-implemented APIs in the observability ecosystem. The ECS mapping is well-documented. Large community prior art for Go clients.

**Lock-in risk:** Low. Elastic and OpenSearch are largely API-compatible. The connector works against either. Open-source deployment option means no vendor dependency.

---

### 5.8 Sumo Logic

**What it is:** Cloud-native log analytics and SIEM. Popular in mid-market, strong in cloud-native organizations. Growing ML-based analytics capability.

**Ingest:** HTTP Source endpoint. Simple POST to a collector URL with JSON payload. One of the simplest enterprise ingest APIs available. Sumo Logic auto-parses JSON fields.

**Cloud SIEM:** Sumo Logic has a Cloud SIEM layer that normalizes ingested data against their own schema (similar to ECS but Sumo-specific). Custom parsers can be built to normalize Argus signals into Sumo's schema, enabling correlation rules and threat intelligence enrichment within Sumo.

**Security:** HTTPS enforced. The HTTP Source URL acts as the credential (it contains a token). Better practice: use a Sumo Logic access key for API-based management. The HTTP Source URL should be treated as a secret and rotated periodically.

**Connector complexity:** Lowest of any enterprise target on this list. No authentication header required beyond the URL. Simple JSON batching. Suitable as a reference implementation connector for testing the framework.

**Lock-in risk:** Medium. Sumo Logic is proprietary SaaS. HTTP Source URLs are Sumo-specific.

---

### 5.9 ArcSight (Micro Focus / OpenText)

**What it is:** One of the oldest enterprise SIEMs, heavily deployed in regulated industries (banking, defense, government). Declining new deployments but significant installed base, particularly in organizations with long procurement cycles and compliance requirements.

**CEF (Common Event Format):** ArcSight's normalized schema. Text-based (pipe-delimited key-value pairs). Well-specified but aging. CEF events are sent over syslog (UDP/TCP) or via ArcSight's proprietary connectors.

**SmartConnectors:** ArcSight's integration framework. Third parties can build SmartConnectors packaged as Java applications. This is the official ArcSight integration path but carries significant complexity (Java, proprietary SDK).

**Alternative approach:** Many ArcSight deployments accept CEF-formatted syslog. A simpler connector could emit CEF-formatted events via TCP syslog (RFC 5424) with TLS. This avoids the SmartConnector framework entirely while remaining compatible with ArcSight's parser.

**Security:** TCP syslog with TLS (RFC 5425). Syslog-ng or rsyslog as a relay is common in enterprise deployments. Mutual TLS supported by ArcSight's syslog receivers in modern deployments.

**Connector complexity:** Medium. CEF formatting is mechanical. TCP/TLS syslog in Go is straightforward. The complexity is ArcSight's deployment heterogeneity — different versions accept different transport methods.

**Lock-in risk:** Low. CEF is a published specification. Syslog is a universal protocol. A CEF-over-syslog connector is essentially a generic enterprise logging connector.

---

### 5.10 IBM QRadar

**What it is:** Enterprise SIEM deployed heavily in financial services, telecommunications, and government. IBM's flagship security platform.

**LEEF (Log Event Extended Format):** IBM's proprietary schema, similar in spirit to ArcSight's CEF. Key-value pair format, pipe-delimited.

**Syslog/DSM:** QRadar ingests data through Device Support Modules (DSMs). Custom DSMs can be built to parse non-standard formats. The simplest path is emitting LEEF-formatted events via syslog and building a lightweight custom DSM definition for QRadar to parse Argus-specific fields.

**QRadar API:** QRadar also has a REST API (Ariel Query Language endpoint) for injecting offenses and reference data. For pushing findings (not raw signals), the API path is cleaner than syslog.

**Connector complexity:** Medium-high. QRadar's DSM framework has historically been complex to develop and test outside a QRadar environment. The syslog+LEEF path is simpler but requires QRadar admin involvement to configure the custom DSM.

**Lock-in risk:** Medium. LEEF is IBM-specific. Syslog is universal. QRadar's complexity is a deterrent for prioritizing this early.

---

## 6. The EUC (End User Computing) Angle

### The Gap

Enterprise platforms do not natively answer the question: what AI tools are employees using, and what organizational data are they sending to those tools? This is a genuine coverage gap that Argus is positioned to address — and it feeds the same connector pipeline, meaning no new infrastructure.

### Signal Sources for EUC AI Monitoring

**Network/Proxy Layer (Highest Fidelity, Least Invasive)**

DNS and proxy telemetry against known AI service FQDNs is the cleanest approach:
- `api.openai.com`, `chatgpt.com` (OpenAI)
- `api.anthropic.com`, `claude.ai` (Anthropic)
- `generativelanguage.googleapis.com`, `gemini.google.com` (Google)
- `api.cohere.com`, `api.mistral.ai`, `api.together.xyz` (alternative providers)
- `github.com/features/copilot`, `copilot.microsoft.com` (Copilot surfaces)
- `perplexity.ai`, `you.com`, `phind.com` (AI search)

This gives volume, frequency, and user/device attribution without content inspection. Proxy with TLS inspection gives request body visibility — useful for DLP classification (is the user pasting source code? PII? financial data?). Proxy with TLS inspection is invasive and carries legal/policy implications by jurisdiction.

**Endpoint Agent Layer**

Process telemetry — which AI-related processes are running (local LLM runtimes, Ollama, LM Studio, copilot extensions). Browser extension detection (ChatGPT browser extensions, Grammarly with AI, etc.). Clipboard monitoring (data copied and pasted into AI interfaces) is technically feasible but carries significant privacy sensitivity and requires explicit policy basis. File access monitoring — are documents being read and presumably pasted into AI tools?

**M365/Google Workspace API Layer**

Microsoft exposes Copilot usage telemetry through the Microsoft 365 Unified Audit Log and the Viva Insights API (for aggregate Copilot usage). This is the least invasive path for M365-centric organizations — no endpoint agent required. Google Workspace similarly exposes some Gemini for Workspace usage through Admin SDK audit logs.

**Browser Telemetry**

Browser extensions deployed via MDM (Intune, Jamf, Google Admin) can capture AI tool interactions at the browser level — page visits, time spent, rough interaction volume. Content inspection requires careful policy consideration. This is the EUC monitoring approach used by tools like Nightfall and Cyberhaven.

### How This Feeds Argus

EUC signals are a distinct signal class from LLM application signals, but they share the same fundamental structure — identity, session, action, data classification, time. The 10-layer taxonomy can accommodate EUC signals with a new top-level classification. The same connector pipeline routes them to the same SIEM targets.

The unique value: SIEM platforms receive correlated data — LLM application telemetry (what the organization's AI systems are doing) plus EUC AI telemetry (what employees are doing with external AI tools) — in a single pipeline. No other platform provides this correlation natively.

### Privacy and Legal Considerations

EUC monitoring depth is jurisdiction-sensitive. In the EU, GDPR constrains employee monitoring significantly — content inspection of communications requires explicit legal basis. In India, the DPDP Act (Digital Personal Data Protection) is evolving. Enterprise policies must define the monitoring scope before technical implementation. The connector layer can be built; what data flows through it is a policy decision outside the scope of this report.

---

## 7. Secure Streaming Architecture Research

### Transport Security Requirements

All connector transport must be TLS 1.3. TLS 1.2 is the minimum floor for platforms that do not yet support 1.3. TLS 1.0 and 1.1 are end-of-life; no connector should negotiate these even if the target accepts them. The connector framework should enforce minimum TLS version at the client level regardless of what the target platform advertises.

**Certificate Validation:** Full chain validation enforced. No `InsecureSkipVerify`. No self-signed certificates without explicit CA pinning configuration. For internal deployments (a self-managed Kafka cluster or OpenSearch cluster), the CA certificate should be configurable and validated — not skipped.

**mTLS (Mutual TLS):** For connector targets that support it (self-managed Kafka, some internal OpenSearch deployments), mTLS provides stronger authentication than token-based auth because both parties authenticate via certificate. This is particularly relevant for the Kafka connector in enterprise environments.

### Authentication Security

**Token-Based Auth (Splunk HEC, LogScale, Sumo Logic):** Tokens are secrets. They must be sourced from a secrets manager at runtime, not stored in config files or environment variables in plain text. Rotation must be supported — the connector framework should support token refresh without restart.

**OAuth 2.0 / Service Account (Sentinel, Chronicle, AWS):** Client credentials flow (no user interaction) is the correct pattern for server-to-server connectors. Client secrets should be rotated on a schedule. Certificate-based service account authentication (where available) is preferred over client secret.

**AWS SigV4:** Stateless request signing. No persistent credential storage if using IAM roles (instance profile, ECS task role, IRSA for EKS). Static access keys are acceptable only in development and should never reach production.

### Secrets Management Options

| Option | Complexity | Appropriate For |
|--------|-----------|-----------------|
| HashiCorp Vault | High setup, low ongoing | Mature enterprise, multi-cloud |
| AWS Secrets Manager | Low setup, AWS-specific | AWS-deployed Argus instances |
| Azure Key Vault | Low setup, Azure-specific | Azure-deployed Argus instances |
| GCP Secret Manager | Low setup, GCP-specific | GCP-deployed Argus instances |
| Kubernetes Secrets (with external-secrets-operator) | Medium | K8s deployments |
| Environment variables (encrypted at rest) | Low | Dev/small deployments only |

For a platform positioning itself as enterprise-ready, the connector framework should support an abstract secrets provider interface — the connector does not care where the secret comes from, only that it receives a valid credential at runtime. This allows operators to use whatever secrets management they already have.

### Signal Integrity

Signals in transit should maintain their taxonomy metadata (signal class, detection tier, confidence score, source application identity). No signal should be stripped of its provenance during connector transformation. OCSF's metadata block is designed for this — it carries original source, transform chain, and schema version.

For high-value deployment scenarios, cryptographic provenance (signing signal batches with the SDK's identity key) should be preserved through the connector transformation. This is a direct extension of Kairos's zero-trust runtime concept applied to the signal pipeline — a downstream platform can verify that a signal originated from a legitimate ArgusSDK instance.

---

## 8. Tech Stack Research — Connector Framework Options

### Option A — Native Go Connector Framework (Recommended)

Build the connector framework as a native Go package within the existing Argus codebase. Each connector is a Go package implementing a common interface. The framework handles lifecycle, health, retry, and backpressure. Connectors are registered at startup based on configuration.

**Why this is right:** Argus is Go-first. No new language dependency. The existing circuit breaker, backpressure, and health monitoring patterns apply directly. No serialization boundary between the pipeline and the connectors. Full control over transport configuration (TLS version, cipher suites, certificate validation).

**Libraries (Go ecosystem):**
- `crypto/tls` — standard library, full TLS control
- `net/http` — standard library, sufficient for REST-based connectors (HEC, Chronicle, Sentinel DCE, Security Hub)
- `franz-go` — best-in-class Kafka producer client, pure Go, no CGO, production-grade TLS/SASL support
- `opensearch-go` — AWS-maintained OpenSearch client for Go
- `elastic/go-elasticsearch` — official Elastic client for Go
- Standard `encoding/json` — sufficient for most targets; `json/v2` (experimental) if performance becomes relevant

**No new runtime dependency.** No additional processes. No new infrastructure. The connector framework compiles into the existing Argus binary.

### Option B — OpenTelemetry Collector as Connector Layer

The OTel Collector is a standalone process with a receiver/processor/exporter pipeline. ArgusSDK would emit OTel-compatible telemetry (OTLP). The Collector routes it to configured exporters (Splunk, Elastic, etc.).

**Pros:** Mature, battle-tested, large exporter ecosystem, no connector code to write for well-supported targets.

**Cons:** Introduces a JVM-free but still separate process dependency. OTel's data model (traces, metrics, logs) does not map cleanly to security signal taxonomy — significant semantic mismatch. Security-specific context (detection tier, threat classification, confidence scores) gets forced into OTel attributes with no semantic meaning to downstream platforms. The exporter ecosystem is observability-focused, not security-focused (no native ASFF, LEEF, CEF, UDM exporters).

**Verdict:** OTel Collector is excellent for operational observability (latency, throughput, error rates of the Argus platform itself). It is a poor fit as the security signal connector layer because the semantic mismatch results in data that reaches the SIEM platform without proper security context.

### Option C — Vector (by Datadog/VectorDev)

Vector is a high-performance data pipeline written in Rust. It has sources, transforms, and sinks. It supports many enterprise targets as sinks. Lightweight, no JVM, good TLS support.

**Pros:** Very capable, performant, lightweight, good sink ecosystem.

**Cons:** Introduces Rust as a runtime dependency in a Go-first codebase. Configuration is VCL/TOML — not Go-native. Vector's transform layer (VRL — Vector Remap Language) is powerful but is another language to learn and maintain. The security-specific schema mapping still requires custom VRL transforms. Operationally, Vector is an additional process to deploy, configure, and monitor.

**Verdict:** Vector is a strong choice if the team wants a pre-built pipeline framework and is comfortable with the operational dependency. For a platform that already has a Go pipeline, it adds complexity without proportionate benefit. Better as an optional integration path for organizations that already run Vector, not as the primary connector mechanism.

### Option D — Fluent Bit

Ultra-lightweight C-based log shipper with a plugin ecosystem. Extremely low resource footprint. Good TLS support. Outputs to most major platforms.

**Pros:** Minimal footprint, widely deployed in Kubernetes environments, many output plugins.

**Cons:** C-based, not Go-native. Plugin development requires C. The output plugin ecosystem covers log shipping but lacks security-schema-aware transformation. Schema mapping would still need to happen before Fluent Bit (in Argus) or via Fluent Bit's filter plugins (Lua or Wasm-based, added complexity).

**Verdict:** Useful as a sidecar for Kubernetes deployments of Argus. Not the right layer for security schema transformation. Could be a supported deployment topology (Argus emits to Fluent Bit OTLP input; Fluent Bit forwards to Splunk/Elastic) but not the primary connector strategy.

---

## 9. Effort Assessment

### Framework Layer

| Component | Effort | Notes |
|-----------|--------|-------|
| Connector interface definition | Low | Clean Go interface design |
| Framework lifecycle (start/stop/health) | Low | Existing Argus patterns apply |
| Retry + backpressure per connector | Low | Circuit breaker pattern already in codebase |
| Dead-letter mechanism | Medium | Requires persistent buffer design |
| Config-driven connector selection | Low | YAML/TOML per connector |
| Secrets provider abstraction | Medium | Interface + one or two concrete implementations |
| TLS enforcement layer | Low | `crypto/tls` config, applies to all connectors |
| OCSF canonical mapper | High | One-time, high-value, defines all subsequent connector scope |

### Per-Connector Effort

| Connector | Schema Work | Transport Work | Auth Work | Total |
|-----------|------------|----------------|-----------|-------|
| Kafka / Redpanda | OCSF → Kafka (JSON) | franz-go producer | SASL/TLS config | Low |
| Splunk HEC | OCSF → CIM fields | HTTP POST | HEC token | Low |
| Sumo Logic | OCSF → Sumo JSON | HTTP POST | URL token | Lowest |
| Elastic / OpenSearch | OCSF → ECS | Bulk API client | API key / cert | Low |
| LogScale | OCSF → LogScale JSON | HTTP POST | Ingest token | Low |
| ArcSight CEF | OCSF → CEF | TCP syslog/TLS | Cert/TLS | Medium |
| AWS Security Hub / Security Lake | OCSF → ASFF / direct OCSF | SigV4 signing | IAM role | Medium |
| Microsoft Sentinel DCE/DCR | OCSF → custom table schema | HTTP POST | Azure AD OAuth | Medium |
| Google Chronicle UDM | OCSF → UDM entities | Chronicle Ingestion API | GCP service account | Medium-High |
| IBM QRadar LEEF | OCSF → LEEF | TCP syslog/TLS | Cert/TLS | Medium-High |

### EUC Signal Layer

| Component | Effort | Notes |
|-----------|--------|-------|
| Proxy/DNS telemetry ingestion | Medium | Standard log parsing, FQDN classification list |
| M365 Audit Log connector | Medium | Microsoft Graph API, delegated permissions |
| Google Workspace Admin SDK | Medium | Service account, domain-wide delegation |
| Endpoint agent extension | High | New agent surface, OS-specific |
| Browser extension | High | Chrome/Edge Manifest V3, policy deployment |

EUC is additive — the connector pipeline is already built by the time EUC signals need routing. The effort is in the signal collection layer, not the routing layer.

---

## 10. What to Evaluate Independently

Based on this research, the following decisions carry material architectural consequences and should be evaluated separately from the connector framework itself:

**1. OCSF adoption as canonical schema.** This is the highest-leverage single decision. If Argus's 10-layer taxonomy maps cleanly to OCSF, the connector scope per platform shrinks to a bounded mapping problem. If the taxonomy has concepts that don't map to OCSF, either the taxonomy evolves or OCSF extensions are defined. Evaluate the taxonomy against OCSF v1.3 schema before committing to the connector framework design.

**2. ArgusSDK serialization format.** Whether the SDK emits JSON, Protobuf, or Avro on its signal streams determines connector input handling. JSON is flexible and debuggable. Protobuf is efficient and schema-enforced. Avro requires a Schema Registry dependency. This decision affects every connector downstream and should be made before connector work begins.

**3. Dead-letter strategy.** What happens when a connector cannot deliver? Options: local disk buffer (durability, ops complexity), ClickHouse sidecar table (reuses existing infrastructure, query visibility), Redis queue (fast, ephemeral, size-limited), or drop-with-metric (simple, data loss accepted). This is a reliability SLA decision, not a technical one.

**4. Connector distribution model.** Are connectors compiled into the Argus binary (simple, monolithic), compiled as Go plugins (dynamic loading, complex build), or run as separate sidecar processes (operationally complex, independently scalable)? The monolithic approach is correct for initial delivery. Plugin or sidecar models are relevant only if third-party connector development becomes a product goal.

**5. ArgusSDK open-source strategy.** If ArgusSDK is open-sourced while ArgusXDR is commercial, the connector layer's position matters — is it part of the open-source SDK (connectors as a commodity, drives adoption) or part of the commercial platform (connectors as a value-add, drives revenue)? This is a product and GTM decision with significant architectural implications.

**6. EUC monitoring legal basis.** Before any EUC signal collection is implemented, the monitoring scope must be defined against applicable legal frameworks (GDPR, DPDP, local employment law). Technical implementation should follow policy definition, not precede it.

---

## 11. Summary Positioning

| Layer | What It Is | Who Needs It |
|-------|------------|--------------|
| ArgusSDK | LLM telemetry agent, runs anywhere | Every LLM app deployment |
| Connector Layer | Signal routing to existing platforms | Enterprises with SIEM investment |
| ArgusXDR | Full LLM security platform | Orgs without SIEM or wanting dedicated LLM plane |
| EUC Signals | Employee AI usage telemetry | Enterprises with AI governance requirements |

The platform's competitive position is clear: no existing SIEM has LLM-native signal taxonomy. No existing LLM observability tool routes to enterprise SIEMs. Argus fills both gaps. The connector layer is the mechanism that converts a standalone platform into enterprise infrastructure — something that sits inside procurement cycles, existing SOC workflows, and existing tool investments rather than competing with them.

The architectural split is viable. The tech stack is mature. The schema work is bounded. The security model is straightforward. The effort is proportionate to the value — the OCSF mapper is the single largest investment, and it pays dividends across every subsequent connector.

---

*End of Report — Research Only. No implementation artifacts produced.*
