package main

import (
	"context"
	"github.com/anchorfree/golang/pkg/jsonlog"
	dckr "github.com/fsouza/go-dockerclient"
	"github.com/gorilla/mux"
	"github.com/jpillora/backoff"
	"github.com/kelseyhightower/envconfig"
	"net/http"
	"strings"
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
	RestartBackOffMin time.Duration `default:"10s" split_words:"true"`
	RestartBackOffMax time.Duration `default:"5m" split_words:"true"`
	DebugMode         bool          `default:"false" split_words:"true"`
	HealMode          bool          `default:"false" split_words:"true"`
	Port              string        `default:"9102"`
}

// App is the main structure of our app
// which holds config values, logger interface
// and shared data.
type App struct {
	config        Config
	log           jsonlog.Logger
	quit          chan struct{}
	database      ContainersList
	patients      PatientsList
	dockerVersion map[string]string
}

// ContainersList is a map of container names to Container struct.
type ContainersList map[string]Container
type Container struct {
	ID           string
	Image        string
	State        string
	Health       string
	Restarts     int
	StuckInspect bool
	Healed       map[string]int
}

// PatientsList is a map of sick containers names to Patient struct.
type PatientsList map[string]*Patient
type Patient struct {
	name               string
	backoff            *backoff.Backoff
	lastRestartAttempt time.Time
	beingTreated       bool
}

// NewApp initializes the App structure,
// initializes the logger and parses the config.
func NewApp() *App {

	log := &jsonlog.StdLogger{}
	log.Init("dkrmgr", false, false, nil)

	app := &App{config: Config{}}
	err := envconfig.Process("dkrmgr", &app.config)
	if err != nil {
		log.Fatal("failed to initialize", err)
	}

	log.Init("dkrmgr", app.config.DebugMode, false, nil)
	app.log = log
	app.database = ContainersList{}
	app.patients = PatientsList{}

	client, err := dckr.NewClient("unix:///" + app.config.DockerSocket)
	if err != nil {
		app.log.Fatal("can't connect to docker daemon", err)
	}

	// It seems unlikely that docker daemon version will change
	// while our app is running, so we just get docker version
	// once upon launch.
	app.dockerVersion = map[string]string{}
	response, err := client.Version()
	if err != nil {
		app.log.Fatal("can't get docker version", err)
	}
	for _, v := range *response {
		if strings.HasPrefix(v, "Version") {
			app.dockerVersion["full"] = v[8:] // remove the prefix "Version=" from the string
		}
	}
	// We need a stripped version too, so we can use it as a pseudo-integer
	// in prometheus metrics.
	r := strings.NewReplacer(".", "", "-ce", "")
	app.dockerVersion["stripped"] = r.Replace(app.dockerVersion["full"])
	return app

}

// GetContainersList is supposed to be called periodically from
// a go routine. On each invokation it gets a list of currently
// present containers via Docker API, parses it and updates
// information in app.database map.
func (app *App) GetContainersList(out chan<- Patient) error {

	client, err := dckr.NewClient("unix:///" + app.config.DockerSocket)
	if err != nil {
		return err
	}
	containers, err := client.ListContainers(dckr.ListContainersOptions{All: true})
	if err != nil {
		return err
	}

	// First we make a map with names of currently existing containers.
	// We'll use it to remove stale entries from our app.database
	existingContainers := map[string]byte{}
	for _, container := range containers {
		existingContainers[container.Names[0]] = 1
	}

	// Now let's update our database
	for _, container := range containers {

		name := container.Names[0]

		c := Container{Image: container.Image, State: container.State, ID: container.ID}
		existingRecord, ok := app.database[name]
		if ok {
			c.Healed = existingRecord.Healed
		} else {
			c.Healed = map[string]int{"success": 0, "fail": 0}
		}

		ctx := context.Background()
		ctx, _ = context.WithTimeout(ctx, app.config.InspectTimeout)
		InspectInfo, err := client.InspectContainerWithContext(container.ID, ctx)

		if err != nil {
			app.log.Error("failed to inspect container "+name, err)
			c.StuckInspect = true
		} else {
			c.Restarts = InspectInfo.RestartCount
			c.Health = InspectInfo.State.Health.Status
			c.StuckInspect = false
		}
		app.database[name] = c

		// If we are in healing mode, we add unhealthy containers
		// to our patients list
		if c.Health == "unhealthy" && app.config.HealMode {
			_, exists := app.patients[name]
			p := &Patient{}
			if !exists {
				b := backoff.Backoff{
					Min:    app.config.RestartBackOffMin,
					Max:    app.config.RestartBackOffMax,
					Factor: 2,
					Jitter: false,
				}
				app.log.Info(name + " container is sick, scheduled for treatment")
				p = &Patient{name: name, backoff: &b, beingTreated: false, lastRestartAttempt: time.Now()}
				app.patients[name] = p
			} else {
				p = app.patients[name]
				app.log.Debug(name + " container is sick, and already been scheduled")
			}
			out <- *p
		}
	}

	// And remove stale entries
	for n, _ := range app.database {
		_, exists := existingContainers[n]
		if !exists {
			delete(app.database, n)
		}
	}

	return nil
}

// HealContainers waits for a new patient from the `in`
// channel, and schedules the restart for each patient.
func (app *App) HealContainers(in <-chan Patient) {

	for p := range in {
		container, ok := app.database[p.name]
		if ok {
			if p.beingTreated {
				app.log.Debug(p.name + ": patient is already being treated")
			} else {
				app.log.Debug(p.name + ": patient is not being treated, starting treatment")
				if container.Health == "unhealthy" {
					p.lastRestartAttempt = time.Now()
					app.patients[p.name].beingTreated = true
					go func() {
						app.log.Debug("sleeping " + p.backoff.Duration().String() + " before restarting " + p.name)
						time.Sleep(p.backoff.Duration())
						client, err := dckr.NewClient("unix:///" + app.config.DockerSocket)
						if err != nil {
							app.log.Error("can't connect to docker daemon to restart "+p.name, err)
						}
						err = client.RestartContainer(container.ID, 10)
						if err != nil {
							app.log.Error("failed to restart container "+p.name, err)
							app.database[p.name].Healed["fail"]++
						} else {
							app.log.Info("restarted the container " + p.name)
							app.database[p.name].Healed["success"]++
						}
						p.beingTreated = false
						app.patients[p.name] = &p
					}()
				} else {
					app.log.Debug(p.name + ": patient is in " + container.Health + " state, skipping restart")
				}
			}
		}
	}
}

// RemoveCuredPatients is supposed to be run in a go routine.
// It checks our patients database, and remove stale entries,
// i.e. containers that are healthy for more than 30 seconds
// since the last healing + cleans up removed containers.
func (app *App) RemoveCuredPatients(ticker *time.Ticker) {

	for {
		select {
		case <-ticker.C:
			for name, patient := range app.patients {
				container, ok := app.database[name]
				if ok {
					// check if it's time to remove the patient from the list
					timeElapsed := int(time.Now().Sub(patient.lastRestartAttempt).Seconds())

					if container.Health == "healthy" && timeElapsed > 30 {
						app.log.Info(name + ": patient is healthy for 30 seconds since last healing, removing from patients list")
						delete(app.patients, name)
					}

				} else {
					app.log.Info(name + ": patient can no longer be seen in containers list, so removing it from patients too")
					delete(app.patients, name)
				}
			}
		case <-app.quit:
			ticker.Stop()
			return
		}
	}

}

func (app *App) UpdateContainersInfo(ticker *time.Ticker, patients chan<- Patient) {

	for {
		select {
		case <-ticker.C:
			err := app.GetContainersList(patients)
			if err != nil {
				app.log.Error("failed to get containers info", err)
			}
		case <-app.quit:
			ticker.Stop()
			return
		}
	}

}

func main() {

	app := NewApp()

	app.log.Info("docker daemon version:" + app.dockerVersion["full"])
	app.log.Info("docker socket path:" + app.config.DockerSocket)
	app.log.Info("scrape interval:" + app.config.ScrapeInterval.String())
	app.log.Info("inspect timeout:" + app.config.InspectTimeout.String())

	t1 := time.NewTicker(app.config.ScrapeInterval)
	t2 := time.NewTicker(app.config.CleanupInterval)
	patients := make(chan Patient, 10)
	go app.UpdateContainersInfo(t1, patients)

	if app.config.HealMode {
		go app.HealContainers(patients)
		go app.RemoveCuredPatients(t2)
	}

	app.log.Info("starting http server on port " + app.config.Port)
	rtr := mux.NewRouter()
	rtr.HandleFunc("/metrics", app.ShowMetrics).Methods("GET")
	http.Handle("/", rtr)
	app.log.Fatal("http server stopped", http.ListenAndServe(":"+app.config.Port, nil))

}
