package main

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/pkg/errors"
)

// configuration captures the plugin's external configuration as exposed in the Mattermost server
// configuration, as well as values computed from the configuration. Any public fields will be
// deserialized from the Mattermost server configuration in OnConfigurationChange.
//
// As plugins are inherently concurrent (hooks being called asynchronously), and the plugin
// configuration can change at any time, access to the configuration must be synchronized. The
// strategy used in this plugin is to guard a pointer to the configuration, and clone the entire
// struct whenever it changes. You may replace this with whatever strategy you choose.
//
// If you add non-reference types to your configuration struct, be sure to rewrite Clone as a deep
// copy appropriate for your types.
type configuration struct {
	Username      string
	TeamsChannels string
	BotUsername   string
	BotIconURL    string
}

// IsValid validates if all the required fields are set.
func (c *configuration) IsValid() error {
	if c.Username == "" {
		return errors.New("Need a Username to make posts as")
	}
	if c.TeamsChannels == "" {
		return errors.New("Need TeamsChannels to post in")
	}
	if strings.Count(c.TeamsChannels, ",")+1 != strings.Count(c.TeamsChannels, "/") {
		return errors.New("TeamsChannels must be in ofrm TeamName/ChannelName")
	}
	if c.BotUsername == "" {
		return errors.New("Need BotUsername")
	}
	if c.BotIconURL == "" {
		return errors.New("Need BotIconURL")
	}

	return nil
}

// Clone shallow copies the configuration. Your implementation may require a deep copy if
// your configuration has reference types.
func (c *configuration) Clone() *configuration {
	var clone = *c
	return &clone
}

// getConfiguration retrieves the active configuration under lock, making it safe to use
// concurrently. The active configuration may change underneath the client of this method, but
// the struct returned by this API call is considered immutable.
func (p *Plugin) getConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()

	if p.configuration == nil {
		return &configuration{}
	}

	return p.configuration
}

// setConfiguration replaces the active configuration under lock.
//
// Do not call setConfiguration while holding the configurationLock, as sync.Mutex is not
// reentrant. In particular, avoid using the plugin API entirely, as this may in turn trigger a
// hook back into the plugin. If that hook attempts to acquire this lock, a deadlock may occur.
//
// This method panics if setConfiguration is called with the existing configuration. This almost
// certainly means that the configuration was modified without being cloned and may result in
// an unsafe access.
func (p *Plugin) setConfiguration(configuration *configuration) {
	p.configurationLock.Lock()
	defer p.configurationLock.Unlock()

	if configuration != nil && p.configuration == configuration {
		// Ignore assignment if the configuration struct is empty. Go will optimize the
		// allocation for same to point at the same memory address, breaking the check
		// above.
		if reflect.ValueOf(*configuration).NumField() == 0 {
			return
		}

		panic("setConfiguration called with the existing configuration")
	}

	p.configuration = configuration
}

// OnConfigurationChange is invoked when configuration changes may have been made.
func (p *Plugin) OnConfigurationChange() error {
	var configuration = new(configuration)

	// Load the public configuration fields from the Mattermost server configuration.
	if err := p.API.LoadPluginConfiguration(configuration); err != nil {
		return errors.Wrap(err, "failed to load plugin configuration")
	}

	p.setConfiguration(configuration)

	if err := configuration.IsValid(); err != nil {
		return err
	}

	user, apErr := p.API.GetUserByUsername(configuration.Username)
	if apErr != nil {
		return fmt.Errorf("Unable to find user with configured username: %v", configuration.Username)
	}
	p.BotUserID = user.Id

	channelsID, err := p.parseChannelsFromConfig(configuration)
	if err != nil {
		return err
	}
	p.ChannelsID = channelsID

	return nil
}

func (p *Plugin) parseChannelsFromConfig(configuration *configuration) ([]string, error) {
	channelsID := make([]string, 0)
	for _, teamsChannels := range strings.Split(configuration.TeamsChannels, ",") {
		v := strings.Split(teamsChannels, "/")
		if len(v) != 2 {
			return channelsID, fmt.Errorf("Bad formatted TeamsChannels: %v", teamsChannels)
		}
		teamName := v[0]
		channelName := v[1]
		team, errC := p.API.GetTeamByName(teamName)
		if errC != nil {
			return channelsID, fmt.Errorf("Unable to find team with configured team: %v", teamName)
		}
		channel, errC := p.API.GetChannelByName(team.Id, channelName, false)
		if errC != nil {
			return channelsID, fmt.Errorf("Unable to find channel with configured channel: %v", channelName)
		}
		channelsID = append(channelsID, channel.Id)
	}
	return channelsID, nil
}
