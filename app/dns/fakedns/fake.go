// +build !confonly

package fakedns

import (
	"context"
	"math"
	"math/big"
	gonet "net"

	"v2ray.com/core/common"
	"v2ray.com/core/common/cache"
	"v2ray.com/core/common/net"
	"v2ray.com/core/features/dns"
)

type Holder struct {
	domainToIP cache.Lru
	nextIP     *big.Int
	ipRange *gonet.IPNet
	config *FakeDnsPool
}

func (*Holder) Type() interface{} {
	return (*dns.FakeDNSEngine)(nil)
}

func (holder *Holder) Start() error {
	return holder.initializeFromConfig()
}

func (holder *Holder) Close() error {
	holder.domainToIP = nil
	holder.nextIP = nil
	holder.ipRange = nil
	return nil
}

func NewFakeDNSHolder() (*Holder, error) {
	var holder *Holder
	var err error

	if holder, err = NewFakeDNSHolderConfigOnly(nil); err != nil {
		return nil, newError("Unable to create Fake Dns Engine").Base(err).AtError()
	}
	err = holder.initialize("240.0.0.0/8", 65535)
	if err != nil {
		return nil, err
	}

	return holder, nil
}

func NewFakeDNSHolderConfigOnly(conf *FakeDnsPool) (*Holder, error) {
	return &Holder{nil, nil, nil, conf}, nil
}

func (holder *Holder) initializeFromConfig() error {
	return holder.initialize(holder.config.IpPool, int(holder.config.LruSize))
}

func (holder *Holder) initialize(ipPoolCidr string, lruSize int) error {
	var ipRange *gonet.IPNet
	var ipaddr gonet.IP
	var currentIP *big.Int
	var err error

	if ipaddr, ipRange, err = gonet.ParseCIDR(ipPoolCidr); err != nil {
		return newError("Unable to parse CIDR for Fake DNS IP assignment").Base(err).AtError()
	}

	currentIP = big.NewInt(0).SetBytes(ipaddr)
	if ipaddr.To4() != nil {
		currentIP = big.NewInt(0).SetBytes(ipaddr.To4())
	}

	ones, bits := ipRange.Mask.Size()
	rooms := bits - ones
	if math.Log2(float64(lruSize)) >= float64(rooms) {
		return newError("LRU size is bigger than subnet size").AtError()
	}
	holder.domainToIP = cache.NewLru(lruSize)
	holder.ipRange = ipRange
	holder.nextIP = currentIP
	return nil
}

// GetFakeIPForDomain check and generate a fake IP for a domain name
func (holder *Holder) GetFakeIPForDomain(domain string) []net.Address {
	if v, ok := holder.domainToIP.Get(domain); ok {
		return []net.Address{v.(net.Address)}
	}
	var ip net.Address
	for {
		ip = net.IPAddress(holder.nextIP.Bytes())

		holder.nextIP = holder.nextIP.Add(holder.nextIP, big.NewInt(1))
		if !holder.ipRange.Contains(holder.nextIP.Bytes()) {
			holder.nextIP = big.NewInt(0).SetBytes(holder.ipRange.IP)
		}

		// if we run for a long time, we may go back to beginning and start seeing the IP in use
		if _, ok := holder.domainToIP.GetKeyFromValue(ip); !ok {
			break
		}
	}
	holder.domainToIP.Put(domain, ip)
	return []net.Address{ip}
}

// GetDomainFromFakeDNS check if an IP is a fake IP and have corresponding domain name
func (holder *Holder) GetDomainFromFakeDNS(ip net.Address) string {
	if !ip.Family().IsIP() || !holder.ipRange.Contains(ip.IP()) {
		return ""
	}
	if k, ok := holder.domainToIP.GetKeyFromValue(ip); ok {
		return k.(string)
	}
	return ""
}

func init() {
	common.Must(common.RegisterConfig((*FakeDnsPool)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		var f *Holder
		var err error
		if f, err = NewFakeDNSHolderConfigOnly(config.(*FakeDnsPool)); err != nil {
			return nil, err
		}
		return f, nil
	}))
}
