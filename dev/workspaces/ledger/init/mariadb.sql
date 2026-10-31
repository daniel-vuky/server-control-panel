CREATE TABLE IF NOT EXISTS titles (
  id INT AUTO_INCREMENT PRIMARY KEY,
  title VARCHAR(120) NOT NULL,
  author VARCHAR(80) NOT NULL
);
INSERT INTO titles (title, author) VALUES
  ('The Pragmatic Programmer', 'Hunt and Thomas'),
  ('Designing Data-Intensive Applications', 'Kleppmann'),
  ('Site Reliability Engineering', 'Beyer et al.');
