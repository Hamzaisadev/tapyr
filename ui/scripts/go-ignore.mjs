// Some npm packages ship Go files (flatted). A go.mod in node_modules
// makes it a module of its own, out of the ./... of the Tapyr module.
import { writeFileSync } from "node:fs";

writeFileSync(new URL("../node_modules/go.mod", import.meta.url), "module ignored/node_modules\n");
