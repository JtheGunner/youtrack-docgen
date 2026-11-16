package netbox

// Minimal views of the NetBox REST objects; unknown fields are ignored.

type ref struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type choice struct {
	Value string `json:"value"`
}

type tag struct {
	Slug string `json:"slug"`
}

type ipRef struct {
	Address string `json:"address"`
}

type site struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Status      choice `json:"status"`
	Region      *ref   `json:"region"`
	Description string `json:"description"`
	Tags        []tag  `json:"tags"`
}

type vlan struct {
	ID     int    `json:"id"`
	VID    int    `json:"vid"`
	Name   string `json:"name"`
	Site   *ref   `json:"site"`
	Status choice `json:"status"`
	Tags   []tag  `json:"tags"`
}

type prefix struct {
	ID          int    `json:"id"`
	Prefix      string `json:"prefix"`
	Status      choice `json:"status"`
	VRF         *ref   `json:"vrf"`
	Site        *ref   `json:"site"`
	VLAN        *ref   `json:"vlan"`
	Role        *ref   `json:"role"`
	Description string `json:"description"`
	Tags        []tag  `json:"tags"`
}

type ipAddress struct {
	ID                 int    `json:"id"`
	Address            string `json:"address"`
	Status             choice `json:"status"`
	VRF                *ref   `json:"vrf"`
	DNSName            string `json:"dns_name"`
	AssignedObjectType string `json:"assigned_object_type"`
	AssignedObjectID   *int   `json:"assigned_object_id"`
	Description        string `json:"description"`
	Tags               []tag  `json:"tags"`
}

// machine covers both dcim devices and virtual machines.
type machine struct {
	ID          int     `json:"id"`
	Name        *string `json:"name"`
	Site        *ref    `json:"site"`
	Cluster     *ref    `json:"cluster"`
	Role        *ref    `json:"role"`
	DeviceRole  *ref    `json:"device_role"` // NetBox < 4.0
	Platform    *ref    `json:"platform"`
	Status      choice  `json:"status"`
	PrimaryIP4  *ipRef  `json:"primary_ip4"`
	PrimaryIP6  *ipRef  `json:"primary_ip6"`
	Description string  `json:"description"`
	Tags        []tag   `json:"tags"`
	DeviceType  *struct {
		Model string `json:"model"`
	} `json:"device_type"`
}

// iface covers both dcim interfaces (device) and VM interfaces
// (virtual_machine).
type iface struct {
	ID             int     `json:"id"`
	Name           string  `json:"name"`
	Device         *ref    `json:"device"`
	VirtualMachine *ref    `json:"virtual_machine"`
	Enabled        bool    `json:"enabled"`
	Type           *choice `json:"type"`
	MACAddress     *string `json:"mac_address"`
	MTU            *int    `json:"mtu"`
	Description    string  `json:"description"`
	Tags           []tag   `json:"tags"`
}
