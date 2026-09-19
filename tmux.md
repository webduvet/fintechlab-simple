I had a crazy idea

I know that tmux is used as shell UI for things like this.

how about we could write cli-version of this simlab tool - especially UI.

we could have some kind of make target which will start new tmux session

the tmux session will have multiple windows containing multiple panes as required by layout.
each window can represent sim service or conrol plane. where we can watch logs, each window could have some kind of controll sidebar wich wouild open/close panes, like extra shell or window to watch logs or status of the service - like incomming and outgoing traffic. 

I totally get it might be more of showcase than real usefulness in this, but hell why not? it would be cool to have completely shell based UI based on tmux as a separate session.


we could even run it remotelly and connect to it via ssh.

the main window would show the overview of all services running, if they are active or inactive, working or iddle. as the ui does now. each service miht have a separate window whre we will be able to interact with it. - watch logs, see the API, list docs, see the api shape, help, set of predefined commants which would run in dedicated shell.

them main window will also have an option to create window for service if user kills it or create new default window as they will be interactable and user could change the shape how they look.
