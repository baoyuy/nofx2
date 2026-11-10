package mcp

// providerRegistry maps provider names to factory functions.
// "custom" is the generic OpenAI-compatible client; URL and model come from
// the user's saved config via SetAPIKey.
var providerRegistry = map[string]func(...ClientOption) AIClient{
	ProviderCustom: func(opts ...ClientOption) AIClient {
		return NewClient(append([]ClientOption{WithProvider(ProviderCustom)}, opts...)...)
	},
}

// RegisterProvider registers a provider factory function.
// Called by provider/payment sub-packages in their init() functions.
func RegisterProvider(name string, factory func(...ClientOption) AIClient) {
	providerRegistry[name] = factory
}

// NewAIClientByProvider creates an AIClient by provider name using the registry.
// Returns nil if the provider is not registered.
func NewAIClientByProvider(name string, opts ...ClientOption) AIClient {
	factory, ok := providerRegistry[name]
	if !ok {
		return nil
	}
	return factory(opts...)
}
