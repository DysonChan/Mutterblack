package discord

import (
	"io"
	"log"
	"regexp"
	"time"
	"errors"

	"github.com/bwmarrin/discordgo"
)

type MessageType string

const (
	// MessageTypeCreate is the message type for message creation.
	MessageTypeCreate MessageType = "create"
	// MessageTypeUpdate is the message type for message updates.
	MessageTypeUpdate = "update"
	// MessageTypeDelete is the message type for message deletion.
	MessageTypeDelete = "delete"
)

type Message interface {
	Channel() string
	UserName() string
	UserID() string
	UserAvatar() string
	Message() string
	RawMessage() string
	MessageID() string
	Type() MessageType
	Timestamp() (time.Time, error)
}

var ErrAlreadyJoined = errors.New("Already joined.")

type DiscordMessage struct {
	Discord          *Discord
	DiscordgoMessage *discordgo.Message
	MessageType      MessageType
	Nick             *string
	Content          *string
}

func (m *DiscordMessage) Channel() string {
	return m.DiscordgoMessage.ChannelID
}

func (m *DiscordMessage) UserName() string {
	me := m.DiscordgoMessage
	if me.Author == nil {
		return ""
	}

	if m.Nick == nil {
		n := m.Discord.NicknameForID(me.Author.ID, me.Author.Username, me.ChannelID)
		m.Nick = &n
	}
	return *m.Nick
}

func (m *DiscordMessage) UserID() string {
	if m.DiscordgoMessage.Author == nil {
		return ""
	}

	return m.DiscordgoMessage.Author.ID
}

func (m *DiscordMessage) UserAvatar() string {
	if m.DiscordgoMessage.Author == nil {
		return ""
	}

	return discordgo.EndpointUserAvatar(m.DiscordgoMessage.Author.ID, m.DiscordgoMessage.Author.Avatar)
}

func (m *DiscordMessage) Message() string {
	if m.Content == nil {
		c := m.DiscordgoMessage.ContentWithMentionsReplaced()
		c = m.Discord.replaceRoleNames(m.DiscordgoMessage, c)
		c = m.Discord.replaceChannelNames(m.DiscordgoMessage, c)

		m.Content = &c
	}
	return *m.Content
}

func (m *DiscordMessage) RawMessage() string {
	return m.DiscordgoMessage.Content
}

func (m *DiscordMessage) MessageID() string {
	return m.DiscordgoMessage.ID
}

func (m *DiscordMessage) Type() MessageType {
	return m.MessageType
}

func (m *DiscordMessage) Timestamp() (time.Time, error) {
	return m.DiscordgoMessage.Timestamp.Parse()
}

type Discord struct {
	args        []interface{}
	messageChan chan Message

	Session             *discordgo.Session
	Sessions            []*discordgo.Session
	OwnerUserID         string
	ApplicationClientID string
}

var channelIDRegex = regexp.MustCompile("<#[0-9]*>")

func (d *Discord) replaceChannelNames(message *discordgo.Message, content string) string {
	return channelIDRegex.ReplaceAllStringFunc(content, func(str string) string {
		c, err := d.Channel(str[2 : len(str)-1])
		if err != nil {
			return str
		}

		return "#" + c.Name
	})
}

var roleIDRegex = regexp.MustCompile("<@&[0-9]*>")

func (d *Discord) replaceRoleNames(message *discordgo.Message, content string) string {
	return roleIDRegex.ReplaceAllStringFunc(content, func(str string) string {
		roleID := str[3 : len(str)-1]

		c, err := d.Channel(message.ChannelID)
		if err != nil {
			return str
		}

		g, err := d.Guild(c.GuildID)
		if err != nil {
			return str
		}

		for _, r := range g.Roles {
			if r.ID == roleID {
				return "@" + r.Name
			}
		}

		return str
	})
}

func (d *Discord) onMessageCreate(s *discordgo.Session, message *discordgo.MessageCreate) {
	if message.Content == "" || (message.Author != nil && message.Author.Bot) {
		return
	}

	d.messageChan <- &DiscordMessage{
		Discord:          d,
		DiscordgoMessage: message.Message,
		MessageType:      MessageTypeCreate,
	}
}

func (d *Discord) onMessageUpdate(s *discordgo.Session, message *discordgo.MessageUpdate) {
	if message.Content == "" || (message.Author != nil && message.Author.Bot) {
		return
	}

	d.messageChan <- &DiscordMessage{
		Discord:          d,
		DiscordgoMessage: message.Message,
		MessageType:      MessageTypeUpdate,
	}
}

func (d *Discord) onMessageDelete(s *discordgo.Session, message *discordgo.MessageDelete) {
	d.messageChan <- &DiscordMessage{
		Discord:          d,
		DiscordgoMessage: message.Message,
		MessageType:      MessageTypeDelete,
	}
}

func (d *Discord) UserName() string {
	if d.Session.State.User == nil {
		return ""
	}
	return d.Session.State.User.Username
}

func (d *Discord) UserID() string {
	if d.Session.State.User == nil {
		return ""
	}
	return d.Session.State.User.ID
}

func (d *Discord) Open() (<-chan Message, error) {
	gateway, err := discordgo.New(d.args...)
	if err != nil {
		return nil, err
	}

	s, err := gateway.GatewayBot()
	if err != nil {
		return nil, err
	}

	d.Sessions = make([]*discordgo.Session, s.Shards)

	for i := 0; i < s.Shards; i++ {
		session, err := discordgo.New(d.args...)
		if err != nil {
			return nil, err
		}
		session.ShardCount = s.Shards
		session.ShardID = i
		session.AddHandler(d.onMessageCreate)
		session.AddHandler(d.onMessageUpdate)
		session.AddHandler(d.onMessageDelete)
		session.State.TrackPresences = false

		d.Sessions[i] = session
	}

	d.Session = d.Sessions[0]

	for i := 0; i < len(d.Sessions); i++ {
		d.Sessions[i].Open()
	}

	return d.messageChan, nil
}

func (d *Discord) IsMe(message Message) bool {
	if d.Session.State.User == nil {
		return false
	}
	return message.UserID() == d.Session.State.User.ID
}

func (d *Discord) SendMessage(channel string, message string) error {
	if channel == "" {
		log.Println("Empty channel could not send message", message)
		return nil
	}

	if _, err := d.Session.ChannelMessageSend(channel, message); err != nil {
		log.Println("Error sending discord message: ", err)
		return err
	}

	return nil
}

func (d *Discord) SendEmbedMessage(channel string, message *discordgo.MessageEmbed) error {
	if channel == "" {
		log.Println("Empty channel could not send message", message)
		return nil
	}

	if _, err := d.Session.ChannelMessageSendEmbed(channel, message); err != nil {
		log.Println("Error sending discord embed message: ", err)
		return err
	}

	return nil
}

func (d *Discord) SendAction(channel string, message string) error {
	if channel == "" {
		log.Println("Empty channel could not send message", message)
		return nil
	}

	p, err := d.UserChannelPermissions(d.UserID(), channel)
	if err != nil {
		return d.SendMessage(channel, message)
	}

	if p&discordgo.PermissionEmbedLinks == discordgo.PermissionEmbedLinks {
		if _, err := d.Session.ChannelMessageSendEmbed(channel, &discordgo.MessageEmbed{
			Color:       d.UserColor(d.UserID(), channel),
			Description: message,
		}); err != nil {
			return err
		}
		return nil
	}

	return d.SendMessage(channel, message)
}

func (d *Discord) DeleteMessage(channel, messageID string) error {
	return d.Session.ChannelMessageDelete(channel, messageID)
}

func (d *Discord) SendFile(channel, name string, r io.Reader) error {
	if _, err := d.Session.ChannelFileSend(channel, name, r); err != nil {
		log.Println("Error sending discord message: ", err)
		return err
	}
	return nil
}

func (d *Discord) BanUser(channel, userID string, duration int) error {
	return d.Session.GuildBanCreate(channel, userID, 0)
}

func (d *Discord) UnbanUser(channel, userID string) error {
	return d.Session.GuildBanDelete(channel, userID)
}

func (d *Discord) Join(join string) error {
	if i, err := d.Session.Invite(join); err == nil {
		if _, err := d.Guild(i.Guild.ID); err == nil {
			return ErrAlreadyJoined
		}
	}

	if _, err := d.Session.InviteAccept(join); err != nil {
		return err
	}
	return nil
}

func (d *Discord) Typing(channel string) error {
	return d.Session.ChannelTyping(channel)
}

func (d *Discord) PrivateMessage(userID string, message string) error {
	c, err := d.Session.UserChannelCreate(userID)
	if err != nil {
		return err
	}
	return d.SendMessage(c.ID, message)
}

func (d *Discord) IsBotOwner(message Message) bool {
	return message.UserID() == d.OwnerUserID
}

func (d *Discord) IsPrivate(message Message) bool {
	c, err := d.Channel(message.Channel())
	return err == nil && c.Type == discordgo.ChannelTypeDM
}

func (d *Discord) IsChannelOwner(message Message) bool {
	c, err := d.Channel(message.Channel())
	if err != nil {
		return false
	}
	g, err := d.Guild(c.GuildID)
	if err != nil {
		return false
	}
	return g.OwnerID == message.UserID() || d.IsBotOwner(message)
}

func (d *Discord) IsModerator(message Message) bool {
	p, err := d.UserChannelPermissions(message.UserID(), message.Channel())
	if err == nil {
		if p&discordgo.PermissionAdministrator == discordgo.PermissionAdministrator || p&discordgo.PermissionManageChannels == discordgo.PermissionManageChannels || p&discordgo.PermissionManageServer == discordgo.PermissionManageServer {
			return true
		}
	}

	return d.IsChannelOwner(message)
}

func (d *Discord) CommandPrefix() string {
	//return fmt.Sprintf("@%s ", d.UserName())
	return "?"
}

func (d *Discord) ChannelCount() int {
	return len(d.Guilds())
}

func (d *Discord) MessageHistory(channel string) []Message {
	c, err := d.Channel(channel)
	if err != nil {
		return nil
	}

	messages := make([]Message, len(c.Messages))
	for i := 0; i < len(c.Messages); i++ {
		messages[i] = &DiscordMessage{
			Discord:          d,
			DiscordgoMessage: c.Messages[i],
			MessageType:      MessageTypeCreate,
		}
	}

	return messages
}

func (d *Discord) GetMessages(channelID string, limit int, beforeID string) ([]Message, error) {
	channelMessages, err := d.Session.ChannelMessages(channelID, limit, beforeID, "", "")
	if err != nil {
		return nil, err
	}

	messages := make([]Message, len(channelMessages))
	for i := 0; i < len(channelMessages); i++ {
		messages[i] = &DiscordMessage{
			Discord:          d,
			DiscordgoMessage: channelMessages[i],
			MessageType:      MessageTypeCreate,
		}
	}

	return messages, err
}

func (d *Discord) Channel(channelID string) (channel *discordgo.Channel, err error) {
	for _, s := range d.Sessions {
		channel, err = s.State.Channel(channelID)
		if err == nil {
			return channel, nil
		}
	}
	return
}

func (d *Discord) Guild(guildID string) (guild *discordgo.Guild, err error) {
	for _, s := range d.Sessions {
		guild, err = s.State.Guild(guildID)
		if err == nil {
			return guild, nil
		}
	}
	return
}

func (d *Discord) Guilds() []*discordgo.Guild {
	guilds := []*discordgo.Guild{}
	for _, s := range d.Sessions {
		guilds = append(guilds, s.State.Guilds...)
	}
	return guilds
}

func (d *Discord) UserChannelPermissions(userID, channelID string) (apermissions int, err error) {
	for _, s := range d.Sessions {
		apermissions, err = s.State.UserChannelPermissions(userID, channelID)
		if err == nil {
			return apermissions, nil
		}
	}
	return
}

func (d *Discord) UserColor(userID, channelID string) int {
	for _, s := range d.Sessions {
		color := s.State.UserColor(userID, channelID)
		if color != 0 {
			return color
		}
	}
	return 0
}

func (d *Discord) Nickname(message Message) string {
	return d.NicknameForID(message.UserID(), message.UserName(), message.Channel())
}

func (d *Discord) NicknameForID(userID, userName, channelID string) string {
	c, err := d.Channel(channelID)
	if err == nil {
		g, err := d.Guild(c.GuildID)
		if err == nil {
			for _, m := range g.Members {
				if m.User.ID == userID {
					if m.Nick != "" {
						return m.Nick
					}
					break
				}
			}
		}
	}
	return userName
}