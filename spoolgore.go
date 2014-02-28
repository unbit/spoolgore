package main

import (
        "flag"
        "time"
        "log"
	"io"
        "io/ioutil"
	"net/smtp"
	"net/mail"
	"strings"
	"bytes"
	"os"
	"path"
	"path/filepath"
	"encoding/json"
	"os/signal"
	"syscall"
)

type Config struct {
	SpoolDir string
	SmtpAddr string
	SmtpUser string
	SmtpPassword string
	PlainUser string
	PlainPassword string
	MD5User string
	MD5Password string
	SmtpAuth smtp.Auth
	Freq int
	MaxAttempts int
	JsonPath string
}

var config Config

/*
	0 -> just pushed
	1 -> still in progress
	2 -> sent
*/

type SentStatus struct {
	Address string
	Status int
	Attempts int
	NextAttempt time.Time
	Error string
}

type MailStatus struct {
	From string
	To []*SentStatus
	Cc []*SentStatus
	Bcc []*SentStatus
	Attempts int
	Enqueued time.Time
}

var status map[string]MailStatus

func parse_options() {
	flag.StringVar(&config.SmtpAddr, "smtpaddr", "127.0.0.1:25", "address of the smtp address to use in the addr:port format")
	flag.StringVar(&config.PlainUser, "smtpuser", "", "username for smtp plain authentication")
	flag.StringVar(&config.PlainPassword, "smtppassword", "", "password for smtp plain authentication")
	flag.StringVar(&config.MD5User, "smtpmd5user", "", "username for smtp cram md5 authentication")
	flag.StringVar(&config.MD5Password, "smtpmd5password", "", "password for smtp cram md5 authentication")
	flag.IntVar(&config.Freq, "freq", 10, "frequency of spool directory scans")
	flag.IntVar(&config.MaxAttempts, "attempts", 100, "max attempts for failed SMTP transactions before giving up")
	flag.StringVar(&config.JsonPath, "json", "", "path of the json status file")
	flag.Parse()
	if flag.NArg() < 1 {
		log.Fatal("please specify a spool directory")
	}
	config.SpoolDir = flag.Args()[0]
}

func send_mail(ss *SentStatus, file string, from string, to string, msg *[]byte) {
	log.Println(file,"sending mail to", to, "attempt:", ss.Attempts)
	dest := []string{to}
	err := smtp.SendMail(config.SmtpAddr, config.SmtpAuth, from, dest, *msg)
	if err != nil {
		log.Println(file,"SMTP error, mail to", to, err)
		ss.Status = 0
		ss.Attempts++
		ss.Error = err.Error()
		if ss.Attempts >= config.MaxAttempts {
			log.Println(file, "max SMTP attempts reached for",to, "... giving up")
			ss.Status = 2
		}
		if (ss.Attempts > 30) {
			ss.NextAttempt = time.Now().Add(time.Duration(30) * time.Duration(60) * time.Second)
		} else {
			ss.NextAttempt = time.Now().Add(time.Duration(ss.Attempts) * time.Duration(60) * time.Second)
		}
		return
	}
	ss.Status = 2
	ss.Error = ""
	log.Println(file, "successfully sent to", to)
}

func spool_flush() {
	num := 0
	now := time.Now()
	for key, _ := range status {
		for i, _ := range status[key].To {
			status[key].To[i].NextAttempt = now
			num += 1
                }
		for i, _ := range status[key].Cc {
			status[key].Cc[i].NextAttempt = now
			num += 1
                }
		for i, _ := range status[key].Bcc {
			status[key].Bcc[i].NextAttempt = now
			num += 1
                }
	}
	log.Printf("spool directory flushed (%d messages in the queue)", num)
	scan_spooldir(config.SpoolDir)
}

func read_json(file string) {
	f, err := os.Open(file)
	if err != nil {
		return
	}
	var buffer bytes.Buffer
	_, err = io.Copy(&buffer, f)
	f.Close()
	if err != nil {
		return
	}
	err = json.Unmarshal(buffer.Bytes(), &status)
	if err != nil {
		log.Println(err)
	} else {
		// reset transactions in progress
		for key, _ := range status {
			for i, ss := range status[key].To {
				if ss.Status == 1 {
					status[key].To[i].Status = 0
				}
			}
			for i, ss := range status[key].Cc {
				if ss.Status == 1 {
					status[key].Cc[i].Status = 0
				}
			}
			for i, ss := range status[key].Bcc {
				if ss.Status == 1 {
					status[key].Bcc[i].Status = 0
				}
			}
		}
	}
}

func write_json(file string, j []byte) {
	f, err := os.Create(file)

	if (err != nil) {
		log.Println(err)
		return
	}

	_, err = f.Write(j)
	if err != nil {
		log.Println(err)
	}
	f.Close()
}

func check_status() {
	changed := false
	for key, _ := range status {
		if _, err := os.Stat(key); os.IsNotExist(err) {
			delete(status, key)
			changed = true
		}
	}

	if changed == true {
		update_json()
	}
}

func update_json() {
	// update status
	js, err := json.MarshalIndent(status, "", "\t")
	if err != nil {
		log.Println(err)
	} else {
		// here we save the file
		write_json(config.JsonPath, js)
	}
}

func try_again(file string, msg *mail.Message) {

	update_json()

	in_progress := false

	mail_status := status[file]

	// rebuild message (strip Bcc)
	var buffer bytes.Buffer
	for key,_ := range msg.Header {
		if key == "Bcc" {
			continue
		}
		buffer.WriteString(key)
		buffer.WriteString(": ")
		header_line := strings.Join(msg.Header[key], ",")
		buffer.WriteString(header_line)
		buffer.WriteString("\r\n")
	}

	buffer.WriteString("\r\n")
	_, err := io.Copy(&buffer, msg.Body)
	if (err != nil) {
		log.Println(file,"unable to reassemble the mail message", err);
		return
	}

	b := buffer.Bytes()

	// manage To
	for i, send_status := range mail_status.To {
		s := send_status.Status
		switch s {
			case 0:
				in_progress = true
				if send_status.NextAttempt.Equal(time.Now()) == true || send_status.NextAttempt.Before(time.Now()) == true {
					// do not use send_status here !!!
					mail_status.To[i].Status = 1
					go send_mail(mail_status.To[i], file, mail_status.From, send_status.Address, &b)
				}
			case 1:
				in_progress = true
		}
	}

	// manage Cc
	for i, send_status := range mail_status.Cc {
		s := send_status.Status
		switch s {
			case 0:
				in_progress = true
				if send_status.NextAttempt.Equal(time.Now()) == true || send_status.NextAttempt.Before(time.Now()) == true {
					// do not use send_status here !!!
					mail_status.Cc[i].Status = 1
					go send_mail(mail_status.Cc[i], file, mail_status.From, send_status.Address, &b)
				}
			case 1:
				in_progress = true
		}
	}

	// manage Bcc
	for i, send_status := range mail_status.Bcc {
		s := send_status.Status
		switch s {
			case 0:
				in_progress = true
				if send_status.NextAttempt.Equal(time.Now()) == true || send_status.NextAttempt.Before(time.Now()) == true {
					// do not use send_status here !!!
					mail_status.Bcc[i].Status = 1
					go send_mail(mail_status.Bcc[i], file, mail_status.From, send_status.Address, &b)
				}
			case 1:
				in_progress = true
		}
	}



	if in_progress == false {
		// first we try to remove the file, on error we avoid to respool the file
		err := os.Remove(file)
		if err != nil {
			log.Println(file,"unable to remove mail file,", err)
			return
		}
		// ok we can now remove the item from the status
		delete(status, file)
		update_json()
	}
}

func parse_mail(file string) {
	f, err := os.Open(file)
	if err != nil {
		log.Println(file,"unable to open mail file,", err)
		return
	}
	defer f.Close()
	msg, err := mail.ReadMessage(f)
	if err != nil {
		log.Println(file,"unable to parse mail file,", err)
		return
	}

	mail_status := MailStatus{}
	mail_status.To = make([]*SentStatus, 0)
	mail_status.Cc = make([]*SentStatus, 0)
	mail_status.Bcc = make([]*SentStatus, 0)

	if _,ok := msg.Header["From"]; ok {
		mail_status.From = msg.Header["From"][0]
	}

	if _,ok := msg.Header["To"]; ok {
		to_addresses, err := msg.Header.AddressList("To")
		if err != nil {
			log.Println(file,"unable to parse mail \"To\" header,", err)
			return
		}
		for _,addr := range to_addresses {
			ss := SentStatus{Address: addr.Address, Status:0, NextAttempt: time.Now()}
			mail_status.To = append(mail_status.To, &ss)
		}
	}

	if _,ok := msg.Header["Cc"]; ok {
		cc_addresses, err := msg.Header.AddressList("Cc")
		if err != nil {
			log.Println(file,"unable to parse mail \"Cc\" header,", err)
			return
		}
		for _,addr := range cc_addresses {
			ss := SentStatus{Address: addr.Address, Status:0, NextAttempt: time.Now()}
			mail_status.Cc = append(mail_status.Cc, &ss)
		}
	}

	if _,ok := msg.Header["Bcc"]; ok {
		bcc_addresses, err := msg.Header.AddressList("Bcc")
		if err != nil {
			log.Println(file,"unable to parse mail \"Bcc\" header,", err)
			return
		}
		for _,addr := range bcc_addresses {
			ss := SentStatus{Address: addr.Address, Status:0, NextAttempt: time.Now()}
			mail_status.Bcc = append(mail_status.Bcc, &ss)
		}
	}

	// is the mail already collected ?
	if _,ok := status[file]; ok {
		try_again(file, msg)
		return
	}

	mail_status.Enqueued = time.Now()
	status[file] = mail_status
	try_again(file, msg)
}

func scan_spooldir(dir string) {
	d, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Println("unable to access spool directory,", err)
		return
	}
	for _, entry := range d {
		if entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		abs,err := filepath.Abs(path.Join(config.SpoolDir, entry.Name()))
		if err != nil {
			log.Println("unable to get absolute path,", err)
			continue
		}
		parse_mail(path.Clean(abs))
	}

	// cleanup the json often
	check_status()
}

func main() {
	parse_options()
	if config.PlainUser != "" {
		config.SmtpAuth = smtp.PlainAuth("", config.PlainUser, config.PlainPassword, strings.Split(config.SmtpAddr, ":")[0])
	} else if config.MD5User != "" {
		config.SmtpAuth = smtp.CRAMMD5Auth(config.MD5User, config.MD5Password)
	}
	log.Printf("--- starting Spoolgore (pid: %d) on directory %s ---", os.Getpid(), config.SpoolDir)
	if config.JsonPath == "" {
		config.JsonPath = path.Join(config.SpoolDir, ".spoolgore.js")
	}
	status = make(map[string]MailStatus)
	read_json(config.JsonPath)
	timer := time.NewTimer(time.Second * time.Duration(config.Freq))
	urg := make(chan os.Signal, 1)
	hup := make(chan os.Signal, 1)
	tstp := make(chan os.Signal, 1)
	signal.Notify(urg, syscall.SIGURG)
	signal.Notify(hup, syscall.SIGHUP)
	signal.Notify(tstp, syscall.SIGTSTP)
	blocked := false
	for {
		select {
			case <- timer.C:
				if blocked == false {
					scan_spooldir(config.SpoolDir)
				}
				timer.Reset(time.Second * time.Duration(config.Freq))
			case <-urg:
				spool_flush()
				blocked = false
			case <-hup:
				read_json(config.JsonPath)
				blocked = false
				log.Println("status reloaded")
			case <-tstp:
				blocked = true
				log.Println("Spoolgore is suspended, send SIGHUP or SIGURG to unpause it")
		}
	}
}
