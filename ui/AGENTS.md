## 🎯 What is Dagu?

Dagu is a **modern, powerful, yet surprisingly simple workflow orchestration engine** that runs as a single binary with zero external dependencies. Born from the frustration of managing hundreds of legacy cron jobs scattered across multiple servers, Dagu brings clarity, visibility, and control to workflow automation.

**The Game Changer**: Unlike traditional workflow engines, Dagu introduces **hierarchical DAG composition** - the ability to nest workflows within workflows to unlimited depth. This transforms how you build and maintain complex systems, enabling true modularity and reusability at scale.

## 🚀 Core Philosophy & Design Principles

### 1. **Local-First Architecture**
- Single binary installation - no databases, message brokers, or external services required
- Works offline and in air-gapped environments
- Sensitive data and workflows stay on your infrastructure
- File-based storage for maximum portability
- Unix socket-based process communication

### 2. **Minimal Configuration**
- Start with just one YAML file
- No complex setup or infrastructure requirements
- Works out of the box with sensible defaults
- Can be running in minutes, not hours or days
- JSON Schema support for IDE auto-completion

### 3. **Language Agnostic**
- Execute ANY command: Python, Bash, Node.js, Go, Rust, or any executable
- No need to learn a new programming language or SDK
- Use your existing scripts and tools as-is
- Perfect for heterogeneous environments
- Shell selection (sh, bash, custom shells, nix-shell with packages)

### 4. **Developer-Friendly**
- Clear, human-readable YAML syntax
- Intuitive web UI with real-time monitoring
- Comprehensive logging with stdout/stderr separation
- Fast onboarding for team members
- Template rendering with Sprig functions

### 5. **Production-Ready**
- Battle-tested in enterprise environments
- Robust error handling and retry mechanisms
- Built-in monitoring and alerting
- Scalable from single workflows to thousands
- Graceful shutdown with configurable cleanup timeouts

## 💪 Comprehensive Features & Capabilities

### 🔄 **Advanced DAG Execution & Control**
- **Directed Acyclic Graphs (DAGs)**: Define complex workflows with dependencies
- **Parallel Execution**: Run multiple steps concurrently with `maxActiveSteps` control
- **Concurrent DAG Runs**: Control parallel runs with `maxActiveRuns`
- **Conditional Execution**: Steps run based on preconditions with:
  - Command exit codes
  - Environment variable checks
  - Command output matching (exact or regex patterns)
  - Command substitution evaluation
- **Dynamic Workflows**: 
  - Pass outputs between steps using `output` field
  - JSON path references for nested data (`${VAR.path.to.value}`)
  - Environment variable expansion
  - Command substitution with backticks
- **🚀 Hierarchical DAG Composition** (Revolutionary Feature!):
  - **Multi-level nesting**: Parent → Child → Grandchild → ... (unlimited depth)
  - **Full hierarchy tracking**: Root, parent, and child relationships maintained
  - **Parameter inheritance**: Pass parameters down the hierarchy chain
  - **Output bubbling**: Access sub DAG outputs in parent workflows
  - **Isolated execution**: Each level runs in its own process
  - **Reusable components**: Build a library of composable workflow modules
  - **Dynamic composition**: Conditionally execute different sub-workflows
- **Step Dependencies**: Define complex dependency graphs between steps

### ⏰ **Sophisticated Scheduling**
- **Cron-based Scheduling**: Standard cron expressions with timezone support
- **Multiple Schedules**: Define arrays of schedule times
- **Start/Stop/Restart Schedules**: Control long-running processes:
  - `start`: When to start the DAG
  - `stop`: When to send stop signals
  - `restart`: When to restart the DAG
- **Skip Redundant Runs**: `skipIfSuccessful` prevents duplicate executions
- **Restart Wait Time**: Configurable delay before restart
- **Schedule-based Preconditions**: Run only on specific days/times

### 🔧 **Powerful Executors**
- **Shell Executor**: Run any command with shell selection:
  - Default shell (`$SHELL` or `sh`)
  - Custom shells (bash, zsh, etc.)
  - nix-shell with package management
- **Docker Executor**: Full container control:
  - Create new containers or exec into existing ones
  - Volume mounts, environment variables, networking
  - Custom entrypoints and working directories
  - Platform selection and image pull policies
- **HTTP Executor**: Advanced API interactions:
  - All HTTP methods with custom headers
  - Query parameters and request bodies
  - Timeout control and silent mode
- **SSH Executor**: Remote command execution:
  - Key-based authentication
  - Custom ports and users
- **Mail Executor**: Email automation:
  - SMTP configuration
  - Multiple recipients
  - File attachments
- **JQ Executor**: JSON processing and transformation

### 🔁 **Advanced Flow Control**
- **Retry Policies**: 
  - Configurable retry limits and intervals
  - Exit code-based retry triggers
  - Exponential backoff support
- **Repeat Policies**:
  - Repeat indefinitely with intervals
  - Conditional repeats based on:
    - Command output matching
    - Exit codes
    - Command evaluation results
- **Continue On Conditions**:
  - Continue on failure or skipped steps
  - Continue based on specific exit codes
  - Continue based on output patterns (regex supported)
  - `markSuccess` to override step status
- **Lifecycle Hooks** (`handlerOn`):
  - `onSuccess`: Execute when DAG succeeds
  - `onFailure`: Execute when DAG fails
  - `onCancel`: Execute when DAG is cancelled
  - `onExit`: Always execute on DAG completion

### 📊 **Enterprise-Grade Features**
- **Queue Management**: 
  - Enqueue DAG runs with priorities
  - Dequeue by name or DAG run ID
  - Queue inspection and management
- **History Retention**: Configurable retention days for execution history
- **Timeout Management**:
  - DAG-level timeout (`timeout`)
  - Step-level cleanup timeout
  - Maximum cleanup time (`maxCleanUpTime`)
- **Delay Controls**:
  - Initial delay before DAG start
  - Inter-step delays
- **Signal Handling**: Custom stop signals per step (`signalOnStop`)
- **Working Directory Control**: Per-step directory configuration

### 🎨 **Modern Web UI**
- **Real-time Dashboard**: 
  - Status metrics with filtering
  - Timeline visualization
  - Date-range filtering
  - DAG-specific views
- **Interactive DAG Editor**: 
  - Edit workflows directly in browser
  - Syntax highlighting
  - Real-time validation
- **Visual Graph Display**: 
  - Horizontal/vertical orientations
  - Real-time status updates
  - Node status indicators
- **Execution History**: 
  - Advanced filtering by date and status
  - Execution timeline views
  - Performance metrics
- **Log Viewer**: 
  - Real-time log streaming
  - Separate stdout/stderr views
  - Log search capabilities
- **Advanced Search**: Find DAGs by name, tags, or content
- **Remote Node Support**: Manage workflows across multiple environments

### 🔒 **Security & Configuration**
- **Authentication**:
  - Basic authentication (username/password)
  - API token authentication
  - TLS/HTTPS support with cert/key files
- **Permissions**:
  - `writeDAGs`: Control DAG creation/editing/deletion
  - `runDAGs`: Control DAG execution
  - API access control
  - UI permission enforcement
- **Configuration Methods**:
  - Environment variables (DAGU_* prefix)
  - Configuration file (`~/.config/dagu/config.yaml`)
  - Base configuration inheritance
  - Per-DAG overrides
  - Command-line arguments
- **Global Settings**:
  - Debug mode toggle
  - Log format (json/text)
  - Timezone configuration
  - Working directory defaults
  - Headless mode for automation
- **Path Configuration**:
  - DAGs directory
  - Log directory
  - Data/history directory
  - Suspend flags directory
  - Admin logs directory
  - Queue directory
  - Process directory
- **UI Customization**:
  - Navbar color and title
  - Log encoding charset
  - Dashboard page limits
  - Latest status display options

### 🛠️ **Variable & Parameter Management**
- **Parameter Types**:
  - Positional parameters (`$1`, `$2`, etc.)
  - Named parameters (`${NAME}`)
  - Map-based parameters
  - Command-line overrides
- **Variable Features**:
  - Environment variable expansion
  - Command substitution with backticks
  - JSON path references (`${VAR.nested.field}`)
  - Default values with overrides
- **Special Variables**:
  - `DAG_NAME`: Current DAG name
  - `DAG_RUN_ID`: Unique execution ID
  - `DAG_RUN_LOG_FILE`: Log file path
  - `DAG_RUN_STEP_NAME`: Current step name
  - `DAG_RUN_STEP_STDOUT_FILE`: Step stdout path
  - `DAG_RUN_STEP_STDERR_FILE`: Step stderr path
- **Template Support**:
  - Sprig template functions
  - Custom template functions
  - Variable interpolation

### 📈 **Operational Excellence**
- **Monitoring & Metrics**:
  - Execution time tracking
  - Resource usage monitoring
  - Performance dashboards
  - Status aggregation
- **Log Management**:
  - Configurable retention policies
  - Log rotation
  - Centralized logging
  - Log file attachments in emails
- **Process Management**:
  - Graceful shutdown
  - Process group management
  - Signal propagation
  - Cleanup timeouts
- **Error Handling**:
  - Detailed error messages
  - Error propagation control
  - Recovery mechanisms
  - Notification on errors

# UI Design Guidelines for Dagu

This document outlines the design principles and guidelines for the Dagu UI based on user feedback and requirements.

## Core Design Principles

### 1. **Developer-Centric Design**
- **Information Dense**: Developers prefer UIs with high information density
- **Minimal Whitespace**: Avoid excessive padding and margins
- **Compact Components**: Use smaller heights for form elements and controls
- **No Unnecessary Decorations**: Focus on functionality over visual embellishments

### 2. **Modern & Simple**
- **Clean Lines**: Use simple borders and clean layouts
- **Consistent Styling**: Maintain uniform background colors and text styles
- **Dark Mode Support**: Ensure all components work well in both light and dark modes
- **Minimal Color Palette**: Use colors purposefully for status and actions

### 3. **Performance & Responsiveness**
- **No Blocking Loading States**: Never hide content with full-page loading indicators
- **Immediate Feedback**: Show previous data while loading new data
- **Smooth Transitions**: Use subtle animations only when necessary
- **Efficient Layouts**: Use flexbox/grid for responsive, space-efficient layouts

## Specific Guidelines

### Loading States
- **NEVER use full-page loading overlays** that hide content
- **AVOID LoadingIndicator components** that block user interaction
- Show stale data while fetching updates rather than hiding everything
- If loading indication is needed, use subtle inline indicators

### Modal Design
- **Compact Headers**: Keep modal headers small and information-dense
- **Minimal Padding**: Use tight spacing (e.g., `p-2` or `p-3` instead of `p-4` or `p-6`)
- **Clear Actions**: Place action buttons prominently with clear labels
- **Keyboard Navigation**: Always support keyboard navigation (arrows, enter, escape)
- **Focus Management**: Don't auto-focus first item unless it makes sense

### Form Elements
- **Small Heights**: Use reduced heights for inputs, selects, and buttons
  - Select boxes: `h-7` or smaller
  - Buttons: `h-7` or `h-8` for standard sizes
  - Inputs: Compact padding (`py-0.5` or `py-1`)
- **Consistent Backgrounds**: Match background colors across related elements
- **No Unnecessary Backgrounds**: Remove backgrounds where they add visual noise

### Tables & Lists
- **Dense Rows**: Minimize row height while maintaining readability
- **Merged Columns**: Combine related data (e.g., error/logs) to save space
- **Proper Text Wrapping**: Always handle long text with `whitespace-normal break-words`
- **Single-Line Metadata**: Keep dates, durations, etc. on one line

### Color & Styling
- **Consistent Metadata Styling**: Use uniform backgrounds for similar information
  - Example: `bg-slate-200 dark:bg-slate-700` for metadata blocks
- **Subtle Borders**: Use `border` class consistently, avoid thick borders
- **Text Hierarchy**: Use size and weight, not excessive color variation
  - Primary text: `text-slate-800 dark:text-slate-200`
  - Secondary text: `text-slate-600 dark:text-slate-400`
  - Muted text: `text-slate-500 dark:text-slate-500`

### Layout Principles
- **Flexbox First**: Use flexbox for dynamic layouts that fill available space
- **Prevent Overflow**: Use `min-h-0` and `overflow-hidden` to prevent layout breaks
- **Account for Fixed Elements**: Always consider headers/footers when setting heights
- **Responsive Breakpoints**: Design mobile-first with sensible breakpoints

### Navigation & Controls
- **Transparent Backgrounds**: Remove unnecessary backgrounds from nav elements
- **Compact Controls**: Keep navigation elements small and unobtrusive
- **Clear Hierarchy**: Use visual hierarchy to guide user attention

## Anti-Patterns to Avoid

1. **Two-Line Displays**: Don't wrap single metadata items across multiple lines
2. **Excessive Whitespace**: Avoid large gaps between elements
3. **Decorative Elements**: Don't add visual elements that don't serve a purpose
4. **Hidden Content**: Never hide content behind loading states unnecessarily
5. **Large Modal Designs**: Avoid modals that take up excessive screen space
6. **Auto-Focus Issues**: Don't auto-focus elements in ways that confuse users

## Component-Specific Guidelines

### Modals
```tsx
// Good: Compact, information-dense modal
<div className="p-3 max-w-2xl max-h-[80vh]">
  <h3 className="text-base font-semibold mb-2">Title</h3>
  <div className="space-y-1">
    {/* Dense content */}
  </div>
</div>

// Bad: Excessive padding and spacing
<div className="p-8 max-w-4xl">
  <h3 className="text-2xl font-bold mb-6">Title</h3>
  <div className="space-y-4">
    {/* Wasteful spacing */}
  </div>
</div>
```

### Data Display
```tsx
// Good: Single-line metadata display
<span className="text-xs">Jun 8, 17:59:40 GMT+9</span>

// Bad: Multi-line metadata display
<div>
  <div>Jun 8, 17:59:40</div>
  <div>GMT+9</div>
</div>
```

### Loading States
```tsx
// Good: Show data immediately
<DAGRunTable dagRuns={data?.dagRuns || []} />

// Bad: Hide content while loading
{isLoading ? (
  <LoadingIndicator />
) : (
  <DAGRunTable dagRuns={data?.dagRuns || []} />
)}
```

## Accessibility Considerations

- Maintain sufficient color contrast in both light and dark modes
- Support keyboard navigation in all interactive components
- Provide clear focus indicators (but not intrusive ones)
- Include appropriate ARIA labels where needed
- Ensure text remains readable at smaller sizes

## Future Considerations

- Consider implementing virtualization for very long lists
- Look into progressive enhancement for complex visualizations
- Maintain consistency as new features are added
- Regular audits for performance and usability

## Development Workflow

**IMPORTANT: DO NOT run `make ui`, `pnpm build`, or any build commands unless explicitly asked by the user.**

**Before committing:**
- Run `make ui` to ensure frontend builds without errors
- Use pnpm for frontend package management

## API Development Guidelines

### Remote Node Parameter Requirement

**CRITICAL: All API calls MUST include the `remoteNode` parameter.**

Dagu supports managing workflows across multiple remote nodes. Every API endpoint that interacts with DAGs, services, or system resources must include the `remoteNode` query parameter to ensure the request is routed to the correct node.

#### Pattern for `useQuery` hooks:
```tsx
const remoteNode = appBarContext.selectedRemoteNode || 'local';

const { data } = useQuery(
  '/endpoint',
  {
    params: {
      query: { remoteNode },
    },
  },
  { refreshInterval: 5000 }
);
```

#### Pattern for `client.POST` / `client.GET` calls:
```tsx
// GET request
const response = await client.GET('/endpoint/{name}', {
  params: { path: { name: id }, query: { remoteNode } },
});

// POST request
const response = await client.POST('/endpoint', {
  params: { query: { remoteNode } },
  body: { ... },
});

// POST with path params
const response = await client.POST('/endpoint/{name}/action', {
  params: { path: { name: id }, query: { remoteNode } },
  body: { ... },
});
```

#### Adding remoteNode to new API endpoints:
When adding new endpoints to `api/v1/api.yaml`, include the RemoteNode parameter reference:
```yaml
/new-endpoint:
  get:
    summary: "Description"
    operationId: "operationName"
    tags:
      - "tag"
    parameters:
      - $ref: "#/components/parameters/RemoteNode"  # Add this line
    responses:
      ...
```

After modifying `api.yaml`, regenerate the API files:
```bash
make api                    # Regenerates Go server code
cd ui && pnpm gen:api       # Regenerates TypeScript types
```

## Git Commit Guidelines
- Keep commit messages to one line unless body is absolutely necessary
- **NEVER EVER use `git add -A` or `git add .`** - ALWAYS stage specific files only
- **CRITICAL: Using `git add -A` is FORBIDDEN. Always use `git add <specific-file>`**
- Follow conventional commit format (fix:, feat:, docs:, etc.)
- For commits fixing bugs or adding features based on user reports add:
  ```
  git commit --trailer "Reported-by:<name>"
  ```
  Where `<name>` is the name of the user
- For commits related to a Github issue, add:
  ```
  git commit --trailer "Github-Issue:#<number>"
  ```
- **NEVER mention co-authored-by or similar aspects**
- **NEVER mention the tool used to create the commit message or PR**
- **NEVER ever include *Generated with* or similar in commit messages**
- **NEVER ever include *Co-Authored-By* or similar in commit messages**