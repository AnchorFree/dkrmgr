package main

import (
	"context"
	"github.com/anchorfree/golang/pkg/jsonlog"
	dckr "github.com/fsouza/go-dockerclient"
	"github.com/gorilla/mux"
	"github.com/kelseyhightower/envconfig"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Config structure will hold all our configuration
// values (processed from environment variables)
// after we initialize it with envconfig.Process
type Config struct {
	DockerSocket   string        `default:"/var/run/docker.sock" split_words:"true"`
	ScrapeInterval time.Duration `default:"1m" split_words:"true"`
	InspectTimeout time.Duration `default:"5s" split_words:"true"`
	DebugMode      bool          `default:"false" split_words:"true"`
	Port           string        `default:"9102"`
}

// App is the main structure of our app
// which holds config values, logger interface
// and shared data.
type App struct {
	config        Config
	log           jsonlog.Logger
	quit          chan struct{}
	database      ContainersInfo
	dockerVersion map[string]string
}

// ContainersInfo is a map of container names to Container struct.
type ContainersInfo map[string]Container

type Container struct {
	Image        string
	State        string
	Health       string
	Restarts     int
	StuckInspect bool
	Healed       int
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
	app.database = ContainersInfo{}

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

// GetContainersInfo is supposed to be called periodically from
// a go routine. On each invokation it gets a list of currently
// present containers via Docker API, parses it and updates
// information in app.database map.
func GetContainersInfo(app *App) error {

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

		c := Container{Image: container.Image, State: container.State}
		existingRecord, ok := app.database[name]
		if ok {
			c.Healed = existingRecord.Healed
		} else {
			c.Healed = 0
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

// ShowMetrics parses app.database and outputs metrics for prometheus
func (app *App) ShowMetrics(w http.ResponseWriter, r *http.Request) {

	output := []string{"# TYPE docker_container_count gauge"}
	output = append(output, "docker_container_count{docker_container_count=\""+strconv.Itoa(len(app.database))+"\"} "+strconv.Itoa(len(app.database)))
	output = append(output, "# TYPE docker_container_healthy gauge")

	for name, container := range app.database {
		healthy := 0
		if container.Health != "" && container.Health != "healthy" && container.Health != "starting" {
			healthy = 1
		}
		if container.State != "running" {
			healthy = 1
		}
		output = append(output, "docker_container_healthy{image_id=\""+container.Image+"\",name=\""+name+"\"} "+strconv.Itoa(healthy))
	}

	output = append(output, "# TYPE docker_container_restart_count counter")
	for name, container := range app.database {
		output = append(output, "docker_container_restart_count{image_id=\""+container.Image+"\",name=\""+name+"\"} "+strconv.Itoa(container.Restarts))
	}
	output = append(output, "# TYPE docker_container_status gauge")
	for name, container := range app.database {
		output = append(output, "docker_container_status{image_id=\""+container.Image+"\",name=\""+name+"\",docker_container_status=\""+container.State+"\"} 1")
	}
	output = append(output, "# TYPE docker_container_stuck_inspect gauge")
	for name, container := range app.database {
		stuck := "0"
		if container.StuckInspect {
			stuck = "1"
		}
		output = append(output, "docker_container_stuck_inspect{image_id=\""+container.Image+"\",name=\""+name+"\"} "+stuck)
	}

	output = append(output, "# TYPE docker_version gauge")
	output = append(output, "docker_version{docker_version=\""+app.dockerVersion["full"]+"\"} "+app.dockerVersion["stripped"])

	w.Write([]byte(strings.Join(output, "\n")))

}

func main() {

	app := NewApp()

	app.log.Info("docker daemon version:" + app.dockerVersion["full"])
	app.log.Info("docker socket path:" + app.config.DockerSocket)
	app.log.Info("scrape interval:" + app.config.ScrapeInterval.String())
	app.log.Info("inspect timeout:" + app.config.InspectTimeout.String())

	ticker := time.NewTicker(app.config.ScrapeInterval)
	go func(app *App) {
		for {
			select {
			case <-ticker.C:
				err := GetContainersInfo(app)
				if err != nil {
					app.log.Error("failed to get containers info", err)
				}
			case <-app.quit:
				ticker.Stop()
				return
			}
		}
	}(app)

	app.log.Info("starting http server on port " + app.config.Port)
	rtr := mux.NewRouter()
	rtr.HandleFunc("/metrics", app.ShowMetrics).Methods("GET")
	http.Handle("/", rtr)
	app.log.Fatal("http server stopped", http.ListenAndServe(":"+app.config.Port, nil))

}
