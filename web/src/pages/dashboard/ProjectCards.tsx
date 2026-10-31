import ProjectCard, { type ProjectItem } from './ProjectCard'

/** ProjectCards is the Dashboard's card view: design 01's grid of project cards. */
export default function ProjectCards({ items }: { items: ProjectItem[] }) {
  return (
    <section
      aria-label="Project list"
      className="grid grid-cols-[repeat(auto-fit,minmax(min(300px,100%),1fr))] gap-3.5"
    >
      {items.map((item) => (
        <ProjectCard key={item.project.id} {...item} />
      ))}
    </section>
  )
}
