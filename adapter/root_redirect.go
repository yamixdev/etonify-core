package adapter

// RootRedirectOwnership is an opt-in resource namespace for a privileged host.
// It is never populated from a sing-box configuration.
type RootRedirectOwnership struct {
	TableName     string
	RedirectTable int
	TProxyTable   int
	RulePriority  int
}
