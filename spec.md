This is a website for computing and displaying the ELO ratings of players in the professional sport of racquetball. Assume a new player starts with an ELO of 1000. Use the standard ELO formula that is used in Chess FIDE to compute ELO. There does not need to be a backend for the website, just a frontend consisting of html/javascript/css that computes the ELO ratings and displays them. The input data can be hardcoded in a file that is loaded dynamically by javascript. Use tailwind css and jquery as needed.

Use golang for converting the input html data into a form that can be easily consumed by the frontend. The golang code should read the input html file, extract the relevant data (players, winners, dates), and output a JSON file that can be loaded by the frontend.

A definition of the formula can be found here:

  `https://en.wikipedia.org/wiki/Performance_rating_(chess)`

The input to the system is a list of matches, where each match consists of two players, the winner of the match and the date of the match.

The system should compute the ELO rating of both players after each match.

There should be a web interface that allows users to view the current ELO ratings of all players, as well as the history of matches and ratings for each player.

There is a sample of the input data in the file kane-waselenchuk.html
