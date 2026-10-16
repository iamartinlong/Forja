'use strict';

const { createServer } = require('./app');

const port = Number(process.env.PORT || 8080);

createServer().listen(port, () => {
  console.log(`[{{slug}}] server listening on :${port}`);
});
