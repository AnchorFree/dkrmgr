package main

import (
	"github.com/jpillora/backoff"
	"sync"
	"time"
)

// Config structure will hold all our configuration
// values (processed from environment variables)
// after we initialize it with envconfig.Process
type Config struct {
	DockerSocket      string        `default:"/var/run/docker.sock" split_words:"true"`
	ScrapeInterval    time.Duration `default:"10s" split_words:"true"`
	InspectTimeout    time.Duration `default:"5s" split_words:"true"`
	CleanupInterval   time.Duration `default:"10s" split_words:"true"`
	RestartBackoffMin time.Duration `default:"10s" split_words:"true"`
	RestartBackoffMax time.Duration `default:"5m" split_words:"true"`
	DebugMode         bool          `default:"false" split_words:"true"`
	HealMode          bool          `default:"false" split_words:"true"`
	Port              string        `default:"9102"`
}

type Container struct {
	ID           string
	Image        string
	State        string
	Health       string
	Restarts     int
	StuckInspect bool
	Healed       map[string]int
}
type Containers struct {
	db    map[string]Container
	mutex sync.RWMutex
}

func (c *Containers) Get(name string) (Container, bool) {
	defer c.mutex.RUnlock()
	c.mutex.RLock()
	container, ok := c.db[name]
	return container, ok
}

func (c *Containers) Put(name string, container Container) {
	defer c.mutex.Unlock()
	c.mutex.Lock()
	c.db[name] = container
}

func (c *Containers) HealSuccess(name string) {
	defer c.mutex.Unlock()
	c.mutex.Lock()
	c.db[name].Healed["success"]++
}

func (c *Containers) HealFail(name string) {
	defer c.mutex.Unlock()
	c.mutex.Lock()
	c.db[name].Healed["fail"]++
}

type Patient struct {
	backoff            *backoff.Backoff
	lastRestartAttempt time.Time
	beingTreated       bool
}
type Patients struct {
	db    map[string]*Patient
	mutex sync.RWMutex
}

func (p *Patients) Get(name string) (*Patient, bool) {
	defer p.mutex.RUnlock()
	p.mutex.RLock()
	patient, ok := p.db[name]
	return patient, ok
}

func (p *Patients) Put(name string, patient *Patient) {
	defer p.mutex.Unlock()
	p.mutex.Lock()
	p.db[name] = patient
}

func (p *Patients) StartTreatment(name string) {
	defer p.mutex.Unlock()
	p.mutex.Lock()
	p.db[name].beingTreated = true
	p.db[name].lastRestartAttempt = time.Now()
}

func (p *Patients) StopTreatment(name string) {
	defer p.mutex.Unlock()
	p.mutex.Lock()
	p.db[name].beingTreated = false
}
