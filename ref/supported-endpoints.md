# ai-gateway-ux

Working repo for documenting UX improvement efforts for Kong AI Gateway running on Kong 3.

## Contents

### [prd/](prd/)
Product requirements documents driving the UX work.
- [prd-ai-gateway-ux.md](prd/prd-ai-gateway-ux.md) — primary PRD for AI Gateway UX improvements
- [prd-ai-manager-2.md](prd/prd-ai-manager-2.md) — PRD for the AI Manager (v2)
- [erd-model-entity.md](prd/erd-model-entity.md) — entity-relationship design for the Model entity

### [ref/](ref/)
Reference material used as input to the PRDs.
- [ai-gateway-api.yaml](ref/ai-gateway-api.yaml) — current AI Gateway API spec
- [example-plugins.yaml](ref/example-plugins.yaml) — sample plugin configurations

### [examples/](examples/)
Concrete configuration examples illustrating proposed UX flows. Each provider directory contains an `ai-gateway-models.yaml` (proposed model entity configuration) and a `kong.yaml` (companion decK declarative config).

- [examples/models/basic/](examples/models/basic/) — minimal multi-provider starter configuration
- [examples/models/openai/](examples/models/openai/) — OpenAI models
- [examples/models/anthropic/](examples/models/anthropic/) — Anthropic models
- [examples/models/bedrock/](examples/models/bedrock/) — AWS Bedrock models
- [examples/models/gemini/](examples/models/gemini/) — Google Gemini models
- [examples/models/vertex/](examples/models/vertex/) — Google Vertex AI models
- [examples/models/cohere/](examples/models/cohere/) — Cohere models
- [examples/models/huggingface/](examples/models/huggingface/) — Hugging Face models


## AI Gateway Capability → Endpoint Mapping
As defined in the OAS - LLM formats describe the incoming API Request format and Capabiltiies describe the types of functionality (chat, image, realtime, etc) enabled for a given model.

### Capabilities to Kong3 route_type and genai_category
Currently, the AI Proxy Advanced includes a couple of fields that inform the AI Gateway's API translation function, which has contributed to a suboptimal UX:
- [route_type](https://developer.konghq.com/plugins/ai-proxy-advanced/reference/#schema--config-targets-route-type) - The model’s operation implementation, for a given provider.
- [genai_category](https://developer.konghq.com/plugins/ai-proxy-advanced/reference/#schema--config-genai-category) - gives a hint on the intent of the inbound request.
The proposed Capabilities field would replace the need for these two fields by providing a more standardized and provider-agnostic way to specify what a model can do. The AI Gateway would then use the declared Capabilities to determine how to route requests to the appropriate provider endpoints, eliminating the need for users to understand the nuances of each provider's API.

#### OpenAI translation vs native provider formats
`route_type` and `genai_category` are primarily used for OpenAI-to-upstream-provider translation. 

Today, with native provider LLM Format passthrough, the defaults (`route_type: llm/v1/chat` and `genai_category: text/generation`) are set.

#### Capability Mappings
The new set of Capabilities, and their respective `route_type` and `genai_category` mappings, are as follows:

**Notes:**
- `audio`: `transcriptions` and `translations` both use the `audio/transcriptions` genai_category
- `rerank`: not a current route_type; by default uses the `llm/v1/chat` route_type

| Capability | Description | route_type (current) | genai_category (current) |
|---|---|---|---|
| generate | Basic text generation, including chat and completion. | `llm/v1/chat`, `llm/v1/completions` | `text/generation` |
| agentic | Agentic capabilities, including tool use and retrieval. | `llm/v1/responses`, `llm/v1/assistants` | `text/generation` |
| realtime | Real-time streaming capabilities. | `realtime/v1/realtime` | `realtime/generation` |
| embeddings | Embedding generation. | `llm/v1/embeddings` | `text/embeddings` |
| image | Image generation and editing. | `image/v1/images/generations`, `image/v1/images/edits` | `image/generation` |
| audio/speech | Audio generation. | `audio/v1/audio/speech` | `audio/speech` |
| audio/transcription | Audio transcription. | `audio/v1/audio/transcriptions` |`audio/transcription` |
| audio/translation | Audio translation. | `audio/v1/audio/translations` | `audio/transcription` |
| video | Video generation. | `video/v1/videos/generations` | `video/generation` |
| rerank | Reranking capabilities. | `llm/v1/chat` | `text/generation` |
| batch | Batch processing capabilities. | `llm/v1/batches` | `text/generation` |
| files | File management capabilities. | `llm/v1/files` | `text/generation` |

### Capabilities per LLM Format
Below is a mapping of supported Capabilities to LLM Formats.

<details>
<summary>OpenAI</summary>

**Notes:**
- `generate`: `/completions` is legacy (no roles/messages). Both are single-inference. Many OpenAI-compatible servers accept with or without `/v1` prefix.
- `agentic`: Assistants (deprecated) was stateful threads + server-side retrieval/code. Responses is the newer converged agentic endpoint.

| Capability | Supported Endpoint(s) | route_type | genai_category |
|---|---|---|---|
| generate | `/chat/completions`, `/completions` | `llm/v1/chat`, `llm/v1/completions` | `text/generation` |
| agentic | `/assistants`, `/responses` | `llm/v1/assistants`, `llm/v1/responses` | `text/generation` |
| realtime | `/realtime` | `llm/v1/realtime` | `text/generation` |
| embeddings | `/embeddings` | `llm/v1/embeddings` | `text/generation` |
| image | `/images/generations`, `/images/edits` | `image/v1/images/generations`, `image/v1/images/edits` | `image/generation` |
| audio/speech | `/audio/speech` | `audio/v1/audio/speech` | `audio/speech` |
| audio/transcription | `/audio/transcriptions` | `audio/v1/audio/transcriptions` | `audio/transcription` |
| audio/translation | `/audio/translations` | `audio/v1/audio/translations` | `audio/transcription` |
| video | `/videos` | `video/v1/videos/generations` | `video/generation` |
| rerank | — | - | - |
| batch | `/batches` | `llm/v1/batches` | `text/generation` |
| files | `/files` | `llm/v1/files` | `text/generation` |
</details>

<details>
<summary>Anthropic</summary>

**Notes:**
- `generate`: Single unified endpoint for chat, tool use, vision, and streaming (via `stream: true`). Client-side tool orchestration.
- `agentic`: No server-orchestrated endpoint. Agentic loops are client-managed using `/v1/messages` with tool use.

| Capability | Supported Endpoint(s) | route_type | genai_category |
|---|---|---|---|
| generate | `/v1/messages` | `llm/v1/chat` | `text/generation` |
| agentic | — | | |
| realtime | — | - | - |
| embeddings | — | - | - |
| image | — | - | - |
| audio | — | - | - |
| video | — | - | - |
| rerank | — | - | - |
| batch | `/v1/messages/batches` | `llm/v1/chat` | `text/generation` |
| files | — | - | - |
</details>

<details>
<summary>Bedrock</summary>

**Notes:**
- `generate` (converse): Standardized chat format. Primary generate endpoints.
- `generate` (invoke): Native provider format. Used for text generation when converse isn't suitable or provider-specific params are needed.
- `agentic`: Server-side RAG: retrieves from Knowledge Bases, then generates. Orchestration is opaque to the caller.

| Capability | Supported Endpoint(s) | route_type | genai_category |
|---|---|---|---|
| generate | `~/model/(?<model_name>[^/]+)/converse(?:-stream)?` | `llm/v1/chat` | `text/generation` |
| generate | `~/model/(?<model_name>[^/]+)/invoke(?:-with-response-stream)?` | `llm/v1/chat` | `text/generation` |
| agentic | `~/model/(?<model_name>[^/]+)/retrieveAndGenerate(?:Stream)?` | `llm/v1/chat` | `text/generation` |
| realtime | — | `llm/v1/chat` | `text/generation` |
| embeddings | `~/model/(?<model_name>[^/]+)/invoke` | `llm/v1/chat` | `text/generation` |
| image | `~/model/(?<model_name>[^/]+)/invoke` | `llm/v1/chat` | `text/generation` |
| audio | `~/model/(?<model_name>[^/]+)/invoke` | `llm/v1/chat` | `text/generation` |
| video | `~/model/(?<model_name>[^/]+)/invoke` | `llm/v1/chat` | `text/generation` |
| rerank | `~/model/(?<model_name>[^/]+)/rerank` | `llm/v1/chat` | `text/generation` |
| batch | `~/model/(?<model_name>[^/]+)/async-invoke`, `/model-invocations` | `llm/v1/chat` | `text/generation` |
| files | — | `llm/v1/chat` | `text/generation` |
</details>

<details>
<summary>Gemini</summary>

**Notes:**
- `generate`: Unified generation endpoint. Handles text, multimodal input, and tool use. Stream variant for SSE.
- `embeddings`: Single and batch embedding in one API surface.
- `files`: Upload endpoint for binary data. `/v1beta/files` for metadata and management.

| Capability | Supported Endpoint(s) | route_type | genai_category |
|---|---|---|---|
| generate | `~/v1beta/models/(?<model_name>[^:/]+):(?:generateContent\|streamGenerateContent)` | `llm/v1/chat` | `text/generation` |
| agentic | — | - | - |
| realtime | — | - | - |
| embeddings | `~/v1beta/models/(?<model_name>[^:/]+):(?:embedContent\|batchEmbedContent)` | `llm/v1/chat` | `text/generation` |
| image | — | - | - |
| audio | — | - | - |
| video | — | - | - |
| rerank | — | - | - |
| batch | `/v1beta/batches` | `llm/v1/chat` | `text/generation` |
| files | `~/(?:upload/)?v1beta/files` | `llm/v1/chat` | `text/generation` |
</details>

<details>
<summary>Vertex AI</summary>

**Notes:**
- `generate`: Same contract as Gemini but within the Vertex project/location namespace.
- `image`: Async generation for Imagen and similar models. Long-running operation pattern (poll for completion).
- `video`: Same async endpoint used for video generation models (e.g. Veo). Model ID determines modality.
- `rerank`: Dedicated ranking endpoint. Separate resource path from model endpoints.
- `batch`: Batch prediction job management. Submit, list, and poll batch inference jobs.
- `files`: Not listed. Files are managed through GCS.

| Capability | Supported Endpoint(s) | route_type | genai_category |
|---|---|---|---|
| generate | `~/v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/(?<model_name>[^:/]+):(?:generateContent\|streamGenerateContent)`, `~/v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/(?<model_name>[^:/]+):predictLongRunning` | `llm/v1/chat` | `text/generation` |
| agentic | — | - | - |
| realtime | — | - | - |
| embeddings | `~/v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/(?<model_name>[^:/]+):(?:embedContent\|batchEmbedContent)` | `llm/v1/chat` | `text/generation` |
| image | `~/v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/(?<model_name>[^:/]+):predictLongRunning` | | |
| audio | — | - | - |
| video | `~/v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/publishers/google/models/(?<model_name>[^:/]+):predictLongRunning` | `llm/v1/chat` | `text/generation` |
| rerank | `~/v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/rankingConfigs/(?<ranking_config>[^:/]+):rank` | `llm/v1/chat` | `text/generation` |
| batch | `~/v1/projects/(?<project_id>[^/]+)/locations/(?<location_id>[^/]+)/batchPredictionJobs` | `llm/v1/chat` | `text/generation` |
| files | — | - | - |
</details>

<details>
<summary>Cohere</summary>

**Notes:**
- `rerank`: v1 is legacy. v2 is current. Query + documents in, relevance scores out.

| Capability | Supported Endpoint(s) | route_type | genai_category |
|---|---|---|---|
| generate | — | - | - |
| agentic | — | - | - |
| realtime | — | - | - |
| embeddings | — | - | - |
| image | — | - | - |
| audio | — | - | - |
| video | — | - | - |
| rerank | `/v1/rerank`, `/v2/rerank` | `llm/v1/chat` | `text/generation` |
| batch | — | - | - |
| files | — | - | - |
</details>

<details>
<summary>Hugging Face</summary>

**Notes:**
- `generate`: Text Generation Inference (TGI) endpoints. Minimal API surface. `/generate_stream` returns SSE.

| Capability | Supported Endpoint(s) | route_type | genai_category |
|---|---|---|---|
| generate | `/generate`, `/generate_stream` | `llm/v1/chat` | `text/generation` |
| agentic | — | - | - |
| realtime | — | - | - |
| embeddings | — | - | - |
| image | — | - | - |
| audio | — | - | - |
| video | — | - | - |
| rerank | — | - | - |
| batch | — | - | - |
| files | — | - | - |
</details>
