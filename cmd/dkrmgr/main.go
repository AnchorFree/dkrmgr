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
	"sync"
	"time"
)

// App is the main structure of our app
// which holds config values, logger interface
// and shared data.
type App struct {
	config        Config
	log           jsonlog.Logger
	quit          chan struct{}
	containers    Containers
	patients      Patients
	dockerVersion map[string]string
	client        *dckr.Client
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
	app.containers = Containers{mutex: sync.RWMutex{}, db: map[string]Container{}}
	app.patients = Patients{mutex: sync.RWMutex{}, db: map[string]*Patient{}}

	client, err := dckr.NewClient("unix:///" + app.config.DockerSocket)
	if err != nil {
		app.log.Fatal("can't connect to docker daemon", err)
	}
	app.client = client
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

// GetContainersList updates app.containers and app.patients maps.
// On each invokation it gets a list of currently
// present containers via Docker API, parses it and updates
// information in app.containers map. If heal mode is on, it also
// adds unhealthy containers to app.patients map, and sends each
// patient's name to out channel.
func (app *App) GetContainersList(out chan<- string) error {

	containers, err := app.client.ListContainers(dckr.ListContainersOptions{All: true})
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

		existingRecord, ok := app.containers.Get(name)
		if ok {
			c.Healed = existingRecord.Healed
			c.Restarts = existingRecord.Restarts
			c.Health = existingRecord.Health
		} else {
			c.Healed = map[string]int{"success": 0, "fail": 0}
		}

		ctx := context.Background()
		ctx, _ = context.WithTimeout(ctx, app.config.InspectTimeout)
		InspectInfo, err := app.client.InspectContainerWithContext(container.ID, ctx)

		if err != nil {
			app.log.Error("failed to inspect container "+name, err)
			c.StuckInspect = true
		} else {
			c.Restarts = InspectInfo.RestartCount
			c.Health = InspectInfo.State.Health.Status
			c.StuckInspect = false
		}
		app.containers.Put(name, c)

		// Add unhealthy containers to the patients list and
		// send their names through out channel if we are in healing mode.
		if c.Health == "unhealthy" && app.config.HealMode {
			_, exists := app.patients.Get(name)
			p := &Patient{}
			if !exists {
				b := backoff.Backoff{
					Min:    app.config.RestartBackoffMin,
					Max:    app.config.RestartBackoffMax,
					Factor: 2,
					Jitter: false,
				}
				app.log.Info(name + " container is sick, scheduled for treatment")
				p = &Patient{backoff: &b, beingTreated: false, lastRestartAttempt: time.Now()}
				app.patients.Put(name, p)
			} else {
				app.log.Debug(name + " container is sick, and already been scheduled")
			}
			out <- name
		}
	}

	// And remove stale entries
	app.containers.mutex.Lock()
	for n, _ := range app.containers.db {
		_, exists := existingContainers[n]
		if !exists {
			delete(app.containers.db, n)
		}
	}
	app.containers.mutex.Unlock()
	return nil

}

// HealContainers waits for a new patient's name from the `in`
// channel, and schedules the restart for each patient.
func (app *App) HealContainers(in <-chan string) {

	for name := range in {
		container, ok := app.containers.Get(name)
		patient, cool := app.patients.Get(name)
		if ok && cool {
			if patient.beingTreated {
				app.log.Debug(name + ": patient is already being treated")
			} else {
				app.log.Debug(name + ": patient is not being treated, starting treatment")
				if container.Health == "unhealthy" {
					app.patients.StartTreatment(name)
					go func() {
						defer app.patients.StopTreatment(name)
						app.log.Debug("sleeping " + patient.backoff.Duration().String() + " before restarting " + name)
						time.Sleep(patient.backoff.Duration())
						err := app.client.RestartContainer(container.ID, 10)
						if err != nil {
							app.log.Error("failed to restart container "+name, err)
							app.containers.HealFail(name)
						} else {

							// Sometimes when you restart a container, although
							// docker API returns the success code, the container
							// is actually not restarted. So we have to introduce
							// some hack to mark this case as failed restart.

							// let's wait a couple of seconds first
							time.Sleep(3 * time.Second)
							// and inspect the restarted container
							ctx := context.Background()
							ctx, _ = context.WithTimeout(ctx, app.config.InspectTimeout)
							InspectInfo, err := app.client.InspectContainerWithContext(container.ID, ctx)
							if err != nil {
								app.log.Error(name+": failed to inspect container after restart, assuming restart failed", err)
								app.containers.HealFail(name)
							} else {
								uptime := time.Now().Sub(InspectInfo.Created).Seconds()
								if uptime > 20 {
									app.log.Debug(name + ": uptime is " + strconv.Itoa(uptime))
									app.log.Info(name + ": uptime is more than 20 seconds, assuming restart failed")
									app.containers.HealFail(name)
								} else {
									app.log.Info("restarted the container " + name)
									app.containers.HealSuccess(name)
								}
							}
						}
					}()
				} else {
					app.log.Debug(name + ": patient is in " + container.Health + " state, skipping restart")
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
			app.patients.mutex.Lock()
			for name, patient := range app.patients.db {
				container, ok := app.containers.Get(name)
				if ok {
					timeElapsed := int(time.Now().Sub(patient.lastRestartAttempt).Seconds())

					if container.Health == "healthy" && timeElapsed > 30 {
						app.log.Info(name + ": patient is healthy for 30 seconds since last healing, removing from patients list")
						delete(app.patients.db, name)
					}

				} else {
					app.log.Info(name + ": patient can no longer be seen in containers list, so removing it from patients too")
					delete(app.patients.db, name)
				}
			}
			app.patients.mutex.Unlock()
		case <-app.quit:
			ticker.Stop()
			return
		}
	}

}

// UpdateContainersInfo is just a go-routine ready wrapper for
// GetContainersList.
func (app *App) UpdateContainersInfo(ticker *time.Ticker, patients chan<- string) {

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
	patients := make(chan string, 10)
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
