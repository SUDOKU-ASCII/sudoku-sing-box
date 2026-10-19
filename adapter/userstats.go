package adapter

// UserStat is a point-in-time view of one user whitelisted by an inbound, for monitoring.
type UserStat struct {
	Name     string
	Blocked  bool
	Exceeded bool
	Conns    int    // open connections
	Up, Down uint64 // wire bytes from and to the user since start
	Used     uint64 // wire bytes this month, counted against Limit
	Limit    uint64 // bytes, 0 = unlimited
}

// UserStatsProvider is implemented by inbounds that enforce a user whitelist.
type UserStatsProvider interface {
	UserStats() []UserStat
}
