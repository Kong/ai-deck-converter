---
title: AI Proxy Advanced Plugin
description: The AI Proxy Advanced plugin lets you transform and proxy requests to
  multiple AI providers and models at the same time. This lets you set up load balancing
  between targets.
url: "/plugins/ai-proxy-advanced/"
content_type: plugin
min_version:
  gateway: '3.8'
tier: ai_gateway_enterprise
products:
- Kong Gateway
- AI Gateway
tools:
- deck
- Admin API
- Konnect API
- KIC
- Operator
- Terraform
tags:
- ai
- ai-proxy
canonical: true
works_on:
- on-prem
- konnect
topologies:
  on_prem:
  - hybrid
  - db-less
  - traditional
  konnect_deployments:
  - hybrid
  - cloud-gateways
  - serverless
publisher: kong-inc
compatible_protocols:
- grpc
- grpcs
- http
- https
- ws
- wss
categories:
- ai


---

# AI Proxy Advanced Plugin












The AI Proxy Advanced plugin lets you transform and proxy requests to multiple AI providers and models at the same time. This lets you set up load balancing between targets.

AI Proxy Advanced plugin accepts requests in one of a few defined and standardized OpenAI formats, translates them to the configured target format, and then transforms the response back into a standard format.

v3.10+ To use AI Proxy Advanced with non-OpenAI format without conversion, see [section below](./#supported-native-llm-formats) for more details.

## Overview of capabilities

AI Proxy Advanced plugin supports capabilities across batch processing, multimodal embeddings, agents, audio, image, streaming, and more, spanning multiple providers.

For Kong Gateway versions 3.6 or earlier:

* **Chat Completions APIs**: Multi-turn conversations with system/user/assistant roles.

* **Completions API**: Generates free-form text from a prompt.

  
  > OpenAI has marked this endpoint as [legacy](https://platform.openai.com/docs/api-reference/completions) and recommends using the [Chat Completions API](https://platform.openai.com/docs/guides/text?api-mode=responses) for developing new applications.

See the following table for capabilities supported in AI Gateway:





### API capability: Chat completions
Description: Generates conversational responses from a sequence of messages using supported LLM providers.
Examples: * [`llm/v1/chat`](./examples/openai-chat-route/)<br>
OpenAI format: Supported

### API capability: Embeddings
Description: Converts text to vector representations for semantic search and similarity matching.
Examples: * [`llm/v1/embeddings`](./examples/embeddings-route-type/)<br>
OpenAI format: Supported

### API capability: Function calling
Description: Allows models to invoke external tools and APIs based on conversation context.
Examples: * "`llm/v1/chat`"
OpenAI format: Supported

### API capability: Assistants and responses
Description: Powers persistent tool-using agents and exposes metadata for debugging and evaluation.
Examples: |
  * [`llm/v1/assistants`](./examples/assistants-route-type/)<br>
  * [`llm/v1/responses`](./examples/responses-route-type/)<br>
OpenAI format: Supported

### API capability: Batches and files
Description: Supports asynchronous bulk LLM requests and file uploads for long documents and structured input.
Examples: |
  * [`llm/v1/batches`](./examples/batches-route-type/)<br>
  * [`llm/v1/files`](./examples/files-route-type/)<br>
  * [Send asynchronous requests to LLMs](/how-to/send-asychronous-llm-requests/)
OpenAI format: Supported

### API capability: Audio
Description: Enables speech-to-text, text-to-speech, and translation for voice applications.
Examples: |
  * [`audio/v1/audio/transcriptions`](./examples/audio-transcription-openai/)<br>
  * [`audio/v1/audio/speech`](./examples/audio-speech-openai/)<br>
  * [`audio/v1/audio/translations`](./examples/audio-translation-openai/)<br>
OpenAI format: Supported

### API capability: Image generation and editing
Description: Generates or modifies images from text prompts.
Examples: |
  * [`image/v1/images/generations`](./examples/image-generation-openai/)<br>
  * [`image/v1/images/edits`](./examples/image-edits-openai/)<br>
OpenAI format: Supported

### API capability: Video generation
Description: Generates videos from text prompts.
Examples: * [`video/v1/videos/generations`](./examples/video-generation-openai/)<br>
OpenAI format: Supported

### API capability: Realtime
Description: Bidirectional WebSocket streaming for low-latency, interactive voice and text applications.
Examples: * [`realtime/v1/realtime`](./examples/realtime-route-openai/)<br>
OpenAI format: Supported

### API capability: AWS Bedrock native APIs
Description: |
  Enables advanced orchestration and real-time RAG via Converse and RetrieveAndGenerate endpoints.
  <br><br>
  Available only when using [native LLM format](./#supported-native-llm-formats) for Bedrock.
Examples: |
  * [`/converse`](./#supported-native-llm-formats)<br>
  * [`/retrieveAndGenerate`](./#supported-native-llm-formats)<br>
OpenAI format: Not Supported

### API capability: Hugging Face native APIs
Description: |
  Provides text generation and streaming using Hugging Face models.
  <br><br>
  Available only when using [native LLM format](./#supported-native-llm-formats) for Hugging Face.
Examples: * [`/generate`](./#supported-native-llm-formats)<br>
OpenAI format: Not Supported

### API capability: Rerank
Description: |
  Reorders documents by relevance for RAG pipelines using Bedrock or Cohere rerank APIs.
  <br><br>
  Available only when using [native LLM format](./#supported-native-llm-formats) for Bedrock and Cohere.
Examples: * [`/rerank`](./#supported-native-llm-formats)<br>
OpenAI format: Not Supported








> The following providers are supported by the legacy [Completions API](https://platform.openai.com/docs/api-reference/completions):
> * OpenAI
> * Azure OpenAI
> * Cohere
> * Llama2
> * Amazon Bedrock
> * Gemini
> * Hugging Face

## Supported AI providers

AI Gateway supports proxying requests to the following AI providers. Each provider page documents supported capabilities, configuration requirements, and provider-specific details.


> For detailed capability support, configuration requirements, and provider-specific limitations, see the individual [provider reference pages](/ai-gateway/ai-providers/).



* [OpenAI](/ai-gateway/ai-providers/openai/)


* [Azure OpenAI](/ai-gateway/ai-providers/azure/)


* [Amazon Bedrock](/ai-gateway/ai-providers/bedrock/)


* [Anthropic](/ai-gateway/ai-providers/anthropic/)


* [Gemini](/ai-gateway/ai-providers/gemini/)


* [Vertex AI](/ai-gateway/ai-providers/vertex/)


* [Cohere](/ai-gateway/ai-providers/cohere/)


* [Mistral](/ai-gateway/ai-providers/mistral/)


* [Hugging Face](/ai-gateway/ai-providers/huggingface/)


* [Llama](/ai-gateway/ai-providers/llama/)


* [xAI](/ai-gateway/ai-providers/xai/)


* [Alibaba Cloud DashScope](/ai-gateway/ai-providers/dashscope/)


* [Cerebras](/ai-gateway/ai-providers/cerebras/)


* [DeepSeek](/ai-gateway/ai-providers/deepseek/)


* [Ollama](/ai-gateway/ai-providers/ollama/)


* [Databricks](/ai-gateway/ai-providers/databricks/)


* [vLLM](/ai-gateway/ai-providers/vllm/)



## How it works

The AI Proxy Advanced plugin will mediate the following for you:

* Request and response formats appropriate for the configured `config.targets[].model.provider` and `config.targets[].route_type`
* The following service request coordinates (unless the model is self-hosted):
  * Protocol
  * Host name
  * Port
  * Path
  * HTTP method
* Authentication on behalf of the Kong API consumer
* Decorating the request with parameters from the `config.targets.model[].options` block, appropriate for the chosen provider
* Recording of usage statistics of the configured LLM provider and model into your selected [Kong log](/plugins/?category=logging) plugin output
* Optionally, additionally recording all post-transformation request and response messages from users, to and from the configured LLM
* Fulfillment of requests to self-hosted models, based on select supported format transformations

Flattening all of the provider formats allows you to standardize the manipulation of the data before and after transmission. It also allows your to provide a choice of LLMs to the Kong Gateway Consumers, using consistent request and response formats, regardless of the backend provider or model.


> v3.11+ AI Proxy Advanced supports REST-based full-text responses, including RESTful endpoints such as `llm/v1/responses`, `llm/v1/files`, `llm/v1/assisstants` and `llm/v1/batches`. RESTful endpoints support CRUD operations— you can `POST` to create a response, `GET` to retrieve it, or `DELETE` to remove it.


## Request and response formats














AI Gateway transforms requests and responses according to the configured [`config.targets[].model.provider`](./reference/#schema--config-targets-model-provider) and [`config.targets[].route_type`](./reference/#schema--config-targets-route-type), using the OpenAI format by default. v3.10+ To use a provider's native format instead, set [`config.llm_format`](./reference/#schema--config-llm-format) to a value other than `openai`. The plugin then passes requests upstream without transformation. See [Supported native LLM formats](#supported-native-llm-formats) for available options.

The following table maps each route type to its [OpenAI API](https://platform.openai.com/docs/api-reference) reference and generative AI category. See the [AI provider reference pages](/ai-gateway/ai-providers/) for provider-specific details.


### `llm/v1/chat`
OpenAI API reference: [Chat completions](https://platform.openai.com/docs/api-reference/chat/create)
Gen AI category: `text/generation`
Min version: 3.6

### `llm/v1/completions`
OpenAI API reference: [Completions](https://platform.openai.com/docs/api-reference/completions)
Gen AI category: `text/generation`
Min version: 3.6

### `llm/v1/embeddings`
OpenAI API reference: [Embeddings](https://platform.openai.com/docs/api-reference/embeddings)
Gen AI category: `text/embeddings`
Min version: 3.11

### `llm/v1/files`
OpenAI API reference: [Files](https://platform.openai.com/docs/api-reference/files)
Gen AI category: N/A
Min version: 3.11

### `llm/v1/batches`
OpenAI API reference: [Batch](https://platform.openai.com/docs/api-reference/batch)
Gen AI category: N/A
Min version: 3.11

### `llm/v1/assistants`
OpenAI API reference: [Assistants](https://platform.openai.com/docs/api-reference/assistants)
Gen AI category: `text/generation`
Min version: 3.11

### `llm/v1/responses`
OpenAI API reference: [Responses](https://platform.openai.com/docs/api-reference/responses)
Gen AI category: `text/generation`
Min version: 3.11

### `realtime/v1/realtime`
OpenAI API reference: [Realtime](https://platform.openai.com/docs/api-reference/realtime)
Gen AI category: `realtime/generation`
Min version: 3.11

### `audio/v1/audio/speech`
OpenAI API reference: [Create speech](https://platform.openai.com/docs/api-reference/audio/createSpeech)
Gen AI category: `audio/speech`
Min version: 3.11

### `audio/v1/audio/transcriptions`
OpenAI API reference: [Create transcription](https://platform.openai.com/docs/api-reference/audio/createTranscription)
Gen AI category: `audio/transcription`
Min version: 3.11

### `audio/v1/audio/translations`
OpenAI API reference: [Create translation](https://platform.openai.com/docs/api-reference/audio/createTranslation)
Gen AI category: `audio/transcription`
Min version: 3.11

### `image/v1/images/generations`
OpenAI API reference: [Create image](https://platform.openai.com/docs/api-reference/images)
Gen AI category: `image/generation`
Min version: 3.11

### `image/v1/images/edits`
OpenAI API reference: [Create image edit](https://platform.openai.com/docs/api-reference/images/createEdit)
Gen AI category: `image/generation`
Min version: 3.11

### `video/v1/videos/generations`
OpenAI API reference: [Create video](https://platform.openai.com/docs/api-reference/videos/create)
Gen AI category: `video/generation`
Min version: 3.13





> Provider-specific parameters can be passed using the `extra_body` field in your request. See the [sample OpenAPI specification](https://github.com/kong/kong/blob/master/spec/fixtures/ai-proxy/oas.yaml) for detailed format examples.

## Supported native LLM formats v3.10+

If you use a [provider’s](/ai-gateway/ai-providers/) native SDK, AI Gateway v3.10+ can proxy the request and return the upstream response without payload format conversion. Set `config.llm_format` to a value other than `openai` to preserve the provider’s native request and response formats.

In this mode, AI Gateway will still provide analytics, logging, and cost calculation.
When `config.llm_format` is set to a native format, only the corresponding provider is supported with its specific APIs.


### [Anthropic](/ai-gateway/ai-providers/anthropic/#supported-native-llm-formats-for-anthropic)
LLM format: `anthropic`
Native capabilities: Messages, batch processing

### [Amazon Bedrock](/ai-gateway/ai-providers/bedrock/#supported-native-llm-formats-for-amazon-bedrock)
LLM format: `bedrock`
Native capabilities: Converse, RAG (RetrieveAndGenerate), reranking, async invocation

### [Cohere](/ai-gateway/ai-providers/cohere/#supported-native-llm-formats-for-cohere)
LLM format: `cohere`
Native capabilities: Reranking

### [Gemini](/ai-gateway/ai-providers/gemini/#supported-native-llm-formats-for-gemini)
LLM format: `gemini`
Native capabilities: Content generation, embeddings, batches, file uploads

### [Vertex AI](/ai-gateway/ai-providers/vertex/#supported-native-llm-formats-for-gemini-vertex)
LLM format: `gemini`
Native capabilities: Content generation, embeddings, batches, reranking, long-running predictions

### [Hugging Face](/ai-gateway/ai-providers/huggingface/#supported-native-llm-formats-for-hugging-face)
LLM format: `huggingface`
Native capabilities: Text generation, streaming




## Load balancing

AI Proxy Advanced supports several load balancing algorithms for distributing requests across AI models:

* **[Round-robin](./examples/round-robin/)**: Weighted traffic distribution.
* **[Consistent-hashing](./examples/consistent-hashing/)**: Sticky sessions based on header values.
* **[Least-connections](./examples/least-connections/)**: Route to backends with spare capacity.
* **[Lowest-latency](./examples/lowest-latency/)**: Route to fastest-responding models.
* **[Lowest-usage](./examples/lowest-usage/)**: Route based on token counts or cost.
* **[Semantic](./examples/semantic/)**: Route based on prompt-to-model similarity.
* **[Priority](./examples/priority/)**: Tiered failover across model groups.


> For detailed algorithm descriptions and selection guidance, see [Load balancing algorithms](/ai-gateway/load-balancing/#load-balancing-algorithms).
>
> For load balancing across Gateway Upstreams and Targets instead of LLMs, see [load balancing with Kong Gateway](/gateway/load-balancing/).

## Retry and fallback

The [AI load balancer](/ai-gateway/load-balancing/) supports configurable retries, timeouts, and failover to different models when a target is unavailable.

v3.10+ Fallback works across targets with any supported format. You can mix providers freely, for example OpenAI and Mistral. Earlier versions require compatible formats between fallback targets. For configuration details, see [Retry and fallback configuration](/ai-gateway/load-balancing/#retry-and-fallback).


> Client errors don't trigger failover.
> To failover on additional error types, set [`config.balancer.failover_criteria`](/plugins/ai-proxy-advanced/reference/#schema--config-balancer-failover-criteria) to include HTTP codes like `http_429` or `http_502`, and `non_idempotent` for POST requests.

## Health check and circuit breaker v3.13+

The [AI load balancer](/ai-gateway/load-balancing/) supports circuit breakers to improve reliability. If a target reaches the failure threshold defined by [`config.balancer.max_fails`](/plugins/ai-proxy-advanced/reference/#schema--config-balancer-max-fails), the load balancer stops routing requests to it until the timeout period ([`config.balancer.fail_timeout`](/plugins/ai-proxy-advanced/reference/#schema--config-balancer-fail-timeout)) elapses.


> For configuration details and behavior examples, see [Circuit breaker](/ai-gateway/load-balancing/#health-check-and-circuit-breaker).

## Templating v3.7+







The plugin allows you to substitute values in the [`config.targets[].model.name`](./reference/#schema--config-targets-model-name) and any parameter under [`config.targets.model[].options`](./reference/#schema--config-targets-model-options)
with specific placeholders, similar to those in the [Request Transformer Advanced](/plugins/request-transformer-advanced/) plugin.

The following templated parameters are available:

* `$(headers.header_name)`: The value of a specific request header.
* `$(uri_captures.path_parameter_name)`: The value of a captured URI path parameter.
* `$(query_params.query_parameter_name)`: The value of a query string parameter.

You can combine these parameters with an OpenAI-compatible SDK in multiple ways using the AI Proxy and AI Proxy Advanced plugins, depending on your specific use case:


### [Select different models dynamically on one provider](./examples/sdk-dynamic-model-selection/)
Description: Allow users to select the target model based on a request header or parameter. Supports flexible routing across different models on the same provider.

### [Use one chat route with dynamic Azure OpenAI deployments](./examples/sdk-azure-one-route/)
Description: Configure a dynamic route to target multiple Azure OpenAI model deployments.

### [Use multiple routes to map mulitple Azure Deployment](./examples/sdk-multiple-azure-deployments/)
Description: Use separate Routes to map Azure OpenAI SDK requests to specific deployments of GPT-3.5 and GPT-4.







## Vector databases

A vector database can be used to store vector embeddings, or numerical representations, of data items. For example, a response would be converted to a numerical representation and stored in the vector database so that it can compare new requests against the stored vectors to find relevant cached items.

The AI Proxy Advanced plugin supports the following vector databases:
* Using `config.vectordb.strategy: redis` and parameters in `config.vectordb.redis`:
  * **[Redis](https://redis.io/docs/latest/stack/search/reference/vectors/)** with Vector Similarity Search (VSS)
  * **[Redis Cloud](https://redis.io/cloud/)**
  * **[Valkey](https://valkey.io/topics/search/)** v3.14+: When you configure `vectordb.strategy: redis`, Kong Gateway queries the server and checks the server name field. If it detects Valkey request, it automatically uses the Valkey-specific driver.
  * Managed Redis with cloud authentication:
    * **AWS ElastiCache** (`auth_provider: aws`)
    * **Azure Managed Redis** (`auth_provider: azure`)
    * **Google Cloud Memorystore** (`auth_provider: gcp`)

    For configuration details, see [Using cloud authentication with Redis](#using-cloud-authentication-with-redis).
* Using `config.vectordb.strategy: pgvector` and parameters in `config.vectordb.pgvector`:
  * **[PostgreSQL with pgvector](https://github.com/pgvector/pgvector)** v3.10+

To learn more about vector databases in AI Gateway, see [Embedding-based similarity matching in Kong AI gateway plugins](/ai-gateway/semantic-similarity/).

## Partials v3.13+

This plugin supports all three AI [Partial](/gateway/entities/partial/) types, which let you define shared configuration once and reuse it across multiple [AI Gateway](/ai-gateway/) plugins.

### `vectordb`
Fields covered: `config.vectordb`

### `embeddings`
Fields covered: `config.embeddings`

### `model`
Fields covered: Each element of `config.targets[]`



A `model` Partial applies to each entry in the `config.targets` array, so you can share one provider configuration across multiple targets.

For setup instructions, see [AI plugin Partials](/gateway/entities/partial/#ai-plugin-partials).

### Using cloud authentication with Redis v3.13+

If your plugin uses a Redis datastore, you can authenticate to it with a cloud Redis provider. 
This allows you to seamlessly rotate credentials without relying on static passwords. 

The following providers are supported:
* AWS ElastiCache
* Azure Managed Redis
* Google Cloud Memorystore (with or without Valkey)



#### AWS instance


You need:
* A running Redis instance on an [AWS ElastiCache instance](https://docs.aws.amazon.com/AmazonElastiCache/latest/dg/auth-iam.html) for Valkey 7.2 or later or ElastiCache for Redis OSS version 7.0 or later
* The [ElastiCache user needs to set "Authentication mode" to "IAM"](https://docs.aws.amazon.com/AmazonElastiCache/latest/dg/auth-iam.html#auth-iam-setup)
* The following policy assigned to the IAM user/IAM role that is used to connect to the ElastiCache:
  ```json
  {
      "Version": "2012-10-17",
      "Statement": [
          {
              "Effect": "Allow",
              "Action": [
                  "elasticache:Connect"
              ],
              "Resource": [
                  "arn:aws:elasticache:ARN_OF_THE_ELASTICACHE",
                  "arn:aws:elasticache:ARN_OF_THE_ELASTICACHE_USER"
              ]
          }
      ]
  }
  ```

To configure cloud authentication with Redis, add the following parameters to your plugin configuration:


```yaml
config:
  vectordb:
    strategy: redis
    redis:
      host: $INSTANCE_ADDRESS
      username: $INSTANCE_USERNAME
      port: 6379
      cloud_authentication:
        auth_provider: aws
        aws_cache_name: $AWS_CACHE_NAME
        aws_is_serverless: false
        aws_region: $AWS_REGION
        aws_access_key_id: $AWS_ACCESS_KEY_ID
        aws_secret_access_key: $AWS_ACCESS_SECRET_KEY
```



Replace the following with your actual values:
* `$INSTANCE_ADDRESS`: The ElastiCache instance address.
* `$INSTANCE_USERNAME`: The ElastiCache username with [IAM Auth mode configured](https://docs.aws.amazon.com/AmazonElastiCache/latest/dg/auth-iam.html#auth-iam-setup).
* `$AWS_CACHE_NAME`: Name of your AWS ElastiCache instance.
* `$AWS_REGION`: Your AWS ElastiCache instance region.
* `$AWS_ACCESS_KEY_ID`: (Optional) Your AWS access key ID. 
* `$AWS_ACCESS_SECRET_KEY`: (Optional) Your AWS secret access key.

#### AWS cluster


You need:
* A running Redis instance on an [AWS ElastiCache cluster](https://docs.aws.amazon.com/AmazonElastiCache/latest/dg/auth-iam.html) for Valkey 7.2 or later or ElastiCache for Redis OSS version 7.0 or later
* The [ElastiCache user needs to set "Authentication mode" to "IAM"](https://docs.aws.amazon.com/AmazonElastiCache/latest/dg/auth-iam.html#auth-iam-setup)
* The following policy assigned to the IAM user/IAM role that is used to connect to the ElastiCache:
  ```json
  {
      "Version": "2012-10-17",
      "Statement": [
          {
              "Effect": "Allow",
              "Action": [
                  "elasticache:Connect"
              ],
              "Resource": [
                  "arn:aws:elasticache:ARN_OF_THE_ELASTICACHE",
                  "arn:aws:elasticache:ARN_OF_THE_ELASTICACHE_USER"
              ]
          }
      ]
  }
  ```

To configure cloud authentication with Redis, add the following parameters to your plugin configuration:


```yaml
config:
  vectordb:
    strategy: redis
    redis:
      cluster_nodes:
      - ip: $CLUSTER_ADDRESS
        port: 6379
      username: $CLUSTER_USERNAME
      port: 6379
      cloud_authentication:
        auth_provider: aws
        aws_cache_name: $AWS_CACHE_NAME
        aws_is_serverless: false
        aws_region: $AWS_REGION 
        aws_access_key_id: $AWS_ACCESS_KEY_ID
        aws_secret_access_key: $AWS_ACCESS_SECRET_KEY 
```



Replace the following with your actual values:
* `$CLUSTER_ADDRESS`: The ElastiCache cluster address.
* `$CLUSTER_USERNAME`: The ElastiCache username with [IAM Auth mode configured](https://docs.aws.amazon.com/AmazonElastiCache/latest/dg/auth-iam.html#auth-iam-setup).
* `$AWS_CACHE_NAME`: Name of your AWS ElastiCache cluster.
* `$AWS_REGION`: Your AWS ElastiCache cluster region.
* `$AWS_ACCESS_KEY_ID`: (Optional) Your AWS access key ID. 
* `$AWS_ACCESS_SECRET_KEY`: (Optional) Your AWS secret access key.

#### Azure instance


You need:
* A running Redis instance on an [Azure Managed Redis instance](https://learn.microsoft.com/en-us/azure/redis/entra-for-authentication) with Entra authentication configured
* Add the [user/service principal/identity to the "Microsoft Entra Authentication Redis user" list](https://learn.microsoft.com/en-us/azure/redis/entra-for-authentication#add-users-or-system-principal-to-your-cache) for the Azure Managed Redis instance

To configure cloud authentication with Redis, add the following parameters to your plugin configuration:


```yaml
config:
  vectordb:
    strategy: redis
    redis:
      host: $INSTANCE_ADDRESS
      username: $INSTANCE_USERNAME
      port: 10000
      cloud_authentication:
        auth_provider: azure
        azure_client_id: $AZURE_CLIENT_ID
        azure_client_secret: $AZURE_CLIENT_SECRET
        azure_tenant_id: $AZURE_TENANT_ID
```



Replace the following with your actual values:
* `$INSTANCE_ADDRESS`: The Azure Managed Redis instance address.
* `$INSTANCE_USERNAME`: The object (principal) ID of the Principal/Identity with essential access.
* `$AZURE_CLIENT_ID`: The client ID of the Principal/Identity.
* `$AZURE_CLIENT_SECRET`: (Optional) The client secret of the Principal/Identity. 
* `$AZURE_TENANT_ID`: (Optional) The tenant ID of the Principal/Identity.


#### Azure cluster


You need:
* A running Redis instance on an [Azure Managed Redis cluster](https://learn.microsoft.com/en-us/azure/redis/entra-for-authentication) with Entra authentication configured
* Add the [user/service principal/identity to the "Microsoft Entra Authentication Redis user" list](https://learn.microsoft.com/en-us/azure/redis/entra-for-authentication#add-users-or-system-principal-to-your-cache) for the Azure Managed Redis instance

To configure cloud authentication with Redis, add the following parameters to your plugin configuration:


```yaml
config:
  vectordb:
    strategy: redis
    redis:
      cluster_nodes:
      - ip: $CLUSTER_ADDRESS
        port: 10000
      username: $CLUSTER_USERNAME
      port: 10000
      cloud_authentication:
        auth_provider: azure
        azure_client_id: $AZURE_CLIENT_ID
        azure_client_secret: $AZURE_CLIENT_SECRET
        azure_tenant_id: $AZURE_TENANT_ID
```



Replace the following with your actual values:
* `$CLUSTER_ADDRESS`: The Azure Managed Redis cluster address.
* `$CLUSTER_USERNAME`: The object (principal) ID of the Principal/Identity with essential access.
* `$AZURE_CLIENT_ID`: The client ID of the Principal/Identity.
* `$AZURE_CLIENT_SECRET`: (Optional) The client secret of the Principal/Identity. 
* `$AZURE_TENANT_ID`: (Optional) The tenant ID of the Principal/Identity.


#### GCP instance


You need:
* A running Redis instance on an [Google Cloud Memorystore instance](https://cloud.google.com/memorystore/docs/cluster/about-iam-auth)
* Assign the principal to the corresponding role: 
    * [Cloud Memorystore Redis DB Connection User(`roles/redis.dbConnectionUser`)](https://docs.cloud.google.com/memorystore/docs/cluster/about-iam-auth) for Memorystore for Redis Cluster
    * [Memorystore DB Connector User (`roles/memorystore.dbConnectionUser`)](https://docs.cloud.google.com/memorystore/docs/valkey/about-iam-auth) for Memorystore for Valkey

To configure cloud authentication with Redis, add the following parameters to your plugin configuration:


```yaml
config:
  vectordb:
    strategy: redis
    redis:
      host: $INSTANCE_ADDRESS
      port: 6379
      cloud_authentication:
        auth_provider: gcp
        gcp_service_account_json: $GCP_SERVICE_ACCOUNT
```



Replace the following with your actual values:
* `$INSTANCE_ADDRESS`: The Memorystore instance address.
* `$GCP_SERVICE_ACCOUNT`: (Optional) The GCP service account JSON.

#### GCP cluster


You need:
* A running Redis instance on an [Google Cloud Memorystore cluster](https://cloud.google.com/memorystore/docs/cluster/about-iam-auth)
* Assign the principal to the corresponding role: 
    * [Cloud Memorystore Redis DB Connection User(`roles/redis.dbConnectionUser`)](https://docs.cloud.google.com/memorystore/docs/cluster/about-iam-auth) for Memorystore for Redis Cluster
    * [Memorystore DB Connector User (`roles/memorystore.dbConnectionUser`)](https://docs.cloud.google.com/memorystore/docs/valkey/about-iam-auth) for Memorystore for Valkey

To configure cloud authentication with Redis, add the following parameters to your plugin configuration:


```yaml
config:
  vectordb:
    strategy: redis
    redis:
      cluster_nodes:
      - ip: $CLUSTER_ADDRESS
        port: 6379 
      port: 6379
      cloud_authentication:
        auth_provider: gcp
        gcp_service_account_json: $GCP_SERVICE_ACCOUNT
```



Replace the following with your actual values:
* `$CLUSTER_ADDRESS`: The Memorystore cluster address.
* `$GCP_SERVICE_ACCOUNT`: The GCP service account JSON.






## FAQs

- Can I override `config.model.name` by specifying a different model name in the request?
  By default, no. The model name must match the one configured in `config.model.name`. If a different model is specified in the request, the plugin returns a 400 error.
  
  However, if you set [`model_alias`](./reference/#schema--config-targets-model-model_alias) on a target, clients can send the alias value in the `model` field instead of the actual provider model name. The plugin matches the request to the target with the corresponding alias. See [Route requests to different models using model aliases](/how-to/route-requests-by-model-alias/) for an example.

- Can I override `temperature`, `top_p`, and `top_k` from the request?

  Yes. The values for [`temperature`](./reference/#schema--config-targets-model-options-temperature), [`top_p`](./reference/#schema--config-targets-model-options-top-p), and [`top_k`](./reference/#schema--config-targets-model-options-top-k) in the request take precedence over those set in `config.targets.model.options`.

- Can I override authentication values from the request?
  Yes, but only if [`config.targets.auth.allow_override`](./reference/#schema--config-targets-auth-allow-override) is set to `true` in the plugin configuration.
  When enabled, this allows request-level auth parameters (such as API keys or bearer tokens) to override the static values defined in the plugin.

- What algorithm does `ai-proxy-advanced` use for selecting the lowest latency target?
  It uses Kong’s built-in load balancing mechanism with the EWMA (Exponentially Weighted Moving Average) algorithm to dynamically route traffic to the backend with the lowest observed latency.

- What is the duration of the learning phase with AI Proxy Advanced?
  There’s no fixed time window. EWMA continuously updates with every response, giving more weight to recent observations. Older latencies decay over time, but still contribute in smaller proportions.

- How does AI Proxy Advanced distribute traffic once a faster model is identified?
  The fastest model gets a majority of traffic, but Kong never sends 100% to a single target unless it's the only one available. In practice, the dominant target may receive ~90–99% of traffic, depending on how much better its EWMA score is.

- Does the system continue testing other targets when the AI Proxy Advanced plugin identifies the fastest model?
  Yes. EWMA ensures all targets continue to receive a small amount of traffic. This ongoing probing lets the system adapt if a previously slower model becomes faster later.

- What’s the approximate percentage of traffic sent to non-dominant targets with AI Proxy Advanced?
  While exact percentages vary with latency gaps, less performant targets typically get between 0.1%–5% of traffic, just enough to keep updating their EWMA score for comparison.

- How do I resolve the MemoryDB error `Number of indexes exceeds the limit`?

  If you see the following error in the logs:
  
  ```sh
  failed to create memorydb instance failed to create index: LIMIT Number of indexes (11) exceeds the limit (10)
  ```
  
  This means that the hardcoded MemoryDB instance limit has been reached.
  To resolve this, create more MemoryDB instances to handle multiple AI Proxy Advanced plugin instances.


## Related Resources

- [AI Gateway](/ai-gateway/)

- [AI Gateway providers](/ai-gateway/ai-providers/)

- [AI Proxy](/plugins/ai-proxy/)

- [Embedding-based similarity matching in Kong AI gateway plugins](/ai-gateway/semantic-similarity/)

