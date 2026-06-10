package kong

// DBLessFormatVersion reuses the declarative config version used by the decK output.
const DBLessFormatVersion = FormatVersion

// DBLessDocument is a flattened DP payload with top-level collections and
// string foreign keys.
type DBLessDocument struct {
	FormatVersion          string                      `yaml:"_format_version"`
	Transform              bool                        `yaml:"_transform"`
	Services               []DBLessService             `yaml:"services,omitempty"`
	Routes                 []DBLessRoute               `yaml:"routes,omitempty"`
	Consumers              []DBLessConsumer            `yaml:"consumers,omitempty"`
	ConsumerGroups         []DBLessConsumerGroup       `yaml:"consumer_groups,omitempty"`
	ConsumerGroupConsumers []DBLessConsumerGroupMember `yaml:"consumer_group_consumers,omitempty"`
	Plugins                []DBLessPlugin              `yaml:"plugins,omitempty"`
	Vaults                 []DBLessVault               `yaml:"vaults,omitempty"`
	AIModels               []DBLessAIModel             `yaml:"ai_models,omitempty"`
	KeyAuthCredentials     []DBLessKeyAuthCredential   `yaml:"keyauth_credentials,omitempty"`
}

func NewDBLessDocument() *DBLessDocument {
	return &DBLessDocument{
		FormatVersion: DBLessFormatVersion,
		Transform:     false,
	}
}

type DBLessService struct {
	ID       string   `yaml:"id"`
	Name     string   `yaml:"name"`
	URL      string   `yaml:"url,omitempty"`
	Host     string   `yaml:"host,omitempty"`
	Port     *int     `yaml:"port,omitempty"`
	Protocol string   `yaml:"protocol,omitempty"`
	Path     string   `yaml:"path,omitempty"`
	Enabled  *bool    `yaml:"enabled,omitempty"`
	Retries  *int     `yaml:"retries,omitempty"`
	Tags     []string `yaml:"tags,omitempty"`
}

type DBLessRoute struct {
	ID                      string              `yaml:"id"`
	Name                    string              `yaml:"name"`
	Service                 string              `yaml:"service,omitempty"`
	Paths                   []string            `yaml:"paths,omitempty"`
	Hosts                   []string            `yaml:"hosts,omitempty"`
	Methods                 []string            `yaml:"methods,omitempty"`
	Protocols               []string            `yaml:"protocols,omitempty"`
	Headers                 map[string][]string `yaml:"headers,omitempty"`
	SNIs                    []string            `yaml:"snis,omitempty"`
	Sources                 []DBLessCIDRPort    `yaml:"sources,omitempty"`
	Destinations            []DBLessCIDRPort    `yaml:"destinations,omitempty"`
	StripPath               *bool               `yaml:"strip_path,omitempty"`
	PreserveHost            *bool               `yaml:"preserve_host,omitempty"`
	HTTPSRedirectStatusCode *int                `yaml:"https_redirect_status_code,omitempty"`
	RegexPriority           *int                `yaml:"regex_priority,omitempty"`
	PathHandling            string              `yaml:"path_handling,omitempty"`
	RequestBuffering        *bool               `yaml:"request_buffering,omitempty"`
	ResponseBuffering       *bool               `yaml:"response_buffering,omitempty"`
	Tags                    []string            `yaml:"tags,omitempty"`
}

type DBLessCIDRPort struct {
	IP   string `yaml:"ip,omitempty"`
	Port *int   `yaml:"port,omitempty"`
}

type DBLessPlugin struct {
	ID            string         `yaml:"id"`
	Name          string         `yaml:"name"`
	Enabled       *bool          `yaml:"enabled,omitempty"`
	Config        map[string]any `yaml:"config,omitempty"`
	Service       string         `yaml:"service,omitempty"`
	Route         string         `yaml:"route,omitempty"`
	Consumer      string         `yaml:"consumer,omitempty"`
	ConsumerGroup string         `yaml:"consumer_group,omitempty"`
	Model         string         `yaml:"model,omitempty"`
	Tags          []string       `yaml:"tags,omitempty"`
}

type DBLessConsumer struct {
	ID       string   `yaml:"id"`
	Username string   `yaml:"username,omitempty"`
	CustomID string   `yaml:"custom_id,omitempty"`
	Tags     []string `yaml:"tags,omitempty"`
}

type DBLessConsumerGroup struct {
	ID   string   `yaml:"id"`
	Name string   `yaml:"name"`
	Tags []string `yaml:"tags,omitempty"`
}

type DBLessConsumerGroupMember struct {
	Consumer      string `yaml:"consumer"`
	ConsumerGroup string `yaml:"consumer_group"`
}

type DBLessVault struct {
	ID          string         `yaml:"id"`
	Prefix      string         `yaml:"prefix"`
	Name        string         `yaml:"name"`
	Description string         `yaml:"description,omitempty"`
	Config      map[string]any `yaml:"config,omitempty"`
	Tags        []string       `yaml:"tags,omitempty"`
}

type DBLessAIModel struct {
	ID    string `yaml:"id"`
	Name  string `yaml:"name"`
	Alias string `yaml:"alias,omitempty"`
}

type DBLessKeyAuthCredential struct {
	ID       string `yaml:"id"`
	Key      string `yaml:"key,omitempty"`
	Consumer string `yaml:"consumer"`
}
