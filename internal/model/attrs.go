package model

// Attribute keys that mean the same for every source. Collectors set them and
// the renderer and the filter read them, so both sides have to use these
// constants. Any other key is free-form and specific to its collector.
const (
	// AttrDescription is a free-text description of the entity.
	AttrDescription = "description"
	// AttrURL is the absolute http(s) URL of the entity's record in its
	// source, shown as a link in the article.
	AttrURL = "url"
	// AttrRepoURL is the absolute http(s) URL of the repository an entity's
	// definition comes from, shown as a link in the article.
	AttrRepoURL = "repo_url"
	// AttrFQDN is the fully qualified domain name of a host.
	AttrFQDN = "fqdn"
	// AttrDNSName is the DNS name registered for an IP address.
	AttrDNSName = "dns_name"
	// AttrPrefix is the CIDR prefix of a network, for example 192.0.2.0/24.
	// Hosts are matched to networks by it.
	AttrPrefix = "prefix"
	// AttrVLAN is the numeric VLAN ID of a network.
	AttrVLAN = "vlan"
	// AttrGateway is the IP address of a network's default gateway.
	AttrGateway = "gateway"
	// AttrExternalIPs is the service attribute that lists, comma separated,
	// the addresses a service is published on outside the cluster: its
	// externalIPs and the IPs of its load balancer (as opposed to its
	// cluster-internal IPs, which never identify a host).
	AttrExternalIPs = "external_ips"
	// AttrVRF is the name of the VRF an IP address entity lives in. Addresses
	// of different VRFs are different addresses even when they read the same.
	AttrVRF = "vrf"
)
