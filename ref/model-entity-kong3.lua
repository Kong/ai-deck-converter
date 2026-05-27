config = {
  type = "record",
  custom_validator = validate_config,
  fields = {
    { source = {re
        type = "string",
        required = true,
        default = "header",
        one_of = { "header", "body" },
        description = "Where the plugin reads the request model from.",
    }, },
    { header_name = typedefs.header_name {
        required = false,
        description = "Header to read when source is 'header'.",
    }, },
    { body_path = {
        type = "string",
        required = false,
        description = "Top-level JSON string field to extract the model from when source is 'body'",
    }, },
    { max_request_body_size = {
       type = "integer",
       required = false,
       default = 8388608, --align with the same one in ai-proxy/ai-proxy-adv plugin
       description = "the max size of the request body that are allowed to read",
    }, },
  },
}
