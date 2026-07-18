'use client';

import * as React from 'react';
import Link from 'next/link';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { AiClassifier, ClassifierInput, InboxSplit } from '@calendium/shared';
import { ArrowLeft, Loader2, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import {
  createClassifierApi,
  deleteClassifierApi,
  fetchClassifiers,
  updateClassifierApi,
} from '@/lib/classifiers-data';
import { DEFAULT_SPLITS } from '@/lib/mail-utils';
import { useInstance } from '@/lib/use-instance';

/** Mirrors the backend's per-user classifier cap (see backend/internal/service/ai.go). */
const MAX_CLASSIFIERS = 20;

interface FormState {
  name: string;
  prompt: string;
  targetSplit: InboxSplit | '';
  labelName: string;
  enabled: boolean;
}

const EMPTY_FORM: FormState = { name: '', prompt: '', targetSplit: '', labelName: '', enabled: true };

function toInput(form: FormState): ClassifierInput {
  return {
    name: form.name.trim(),
    prompt: form.prompt.trim(),
    targetSplit: form.targetSplit || undefined,
    labelName: form.labelName.trim() || undefined,
    enabled: form.enabled,
  };
}

/** A classifier without a target must at least carry a label, or it matches
 * mail and does nothing observable. */
function validate(form: FormState): string | null {
  if (!form.name.trim()) return 'Give the classifier a name.';
  if (!form.prompt.trim()) return 'Describe what this classifier should match.';
  if (!form.targetSplit && !form.labelName.trim()) {
    return 'Choose a target split or a label — at least one is required.';
  }
  return null;
}

export default function ClassifiersPage() {
  const aiEnabled = useInstance().data?.features.ai ?? false;
  const queryClient = useQueryClient();
  const classifiersQuery = useQuery({ queryKey: ['classifiers'], queryFn: fetchClassifiers });
  const classifiers = classifiersQuery.data ?? [];

  const [formOpen, setFormOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<AiClassifier | null>(null);
  const [form, setForm] = React.useState<FormState>(EMPTY_FORM);
  const [formError, setFormError] = React.useState<string | null>(null);

  function openCreate() {
    setEditing(null);
    setForm(EMPTY_FORM);
    setFormError(null);
    setFormOpen(true);
  }

  function openEdit(classifier: AiClassifier) {
    setEditing(classifier);
    setForm({
      name: classifier.name,
      prompt: classifier.prompt,
      targetSplit: classifier.targetSplit ?? '',
      labelName: classifier.labelName ?? '',
      enabled: classifier.enabled,
    });
    setFormError(null);
    setFormOpen(true);
  }

  const save = useMutation({
    mutationFn: async () => {
      const input = toInput(form);
      return editing ? updateClassifierApi(editing.id, input) : createClassifierApi(input);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['classifiers'] });
      toast.success(editing ? 'Classifier updated' : 'Classifier created');
      setFormOpen(false);
    },
    onError: () =>
      toast.error(editing ? 'Could not update the classifier' : 'Could not create the classifier'),
  });

  const remove = useMutation({
    mutationFn: (id: string) => deleteClassifierApi(id),
    onSuccess: (_data, id) => {
      queryClient.setQueryData<AiClassifier[]>(['classifiers'], (prev) =>
        prev?.filter((c) => c.id !== id)
      );
      toast.success('Classifier deleted');
    },
    onError: () => toast.error('Could not delete the classifier'),
  });

  const toggleEnabled = useMutation({
    mutationFn: (classifier: AiClassifier) =>
      updateClassifierApi(classifier.id, {
        name: classifier.name,
        prompt: classifier.prompt,
        targetSplit: classifier.targetSplit,
        labelName: classifier.labelName,
        enabled: !classifier.enabled,
      }),
    onSuccess: (updated) => {
      queryClient.setQueryData<AiClassifier[]>(['classifiers'], (prev) =>
        prev?.map((c) => (c.id === updated.id ? updated : c))
      );
    },
    onError: () => toast.error('Could not update the classifier'),
  });

  function handleSubmit() {
    const validationError = validate(form);
    if (validationError) {
      setFormError(validationError);
      return;
    }
    setFormError(null);
    save.mutate();
  }

  const atCap = classifiers.length >= MAX_CLASSIFIERS;

  if (!aiEnabled) {
    return (
      <div className="mx-auto max-w-2xl p-6">
        <p className="text-muted-foreground text-sm">
          AI classifiers require AI features to be enabled on this server.
        </p>
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-2xl flex-col gap-4 p-6">
      <div className="flex items-center gap-2">
        <Button variant="ghost" size="icon" className="size-7" asChild>
          <Link href="/settings" aria-label="Back to settings">
            <ArrowLeft className="size-4" />
          </Link>
        </Button>
        <h1 className="text-lg font-semibold">AI classifiers</h1>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Custom classifiers</CardTitle>
          <CardDescription>
            Natural-language rules applied to incoming mail — route matching threads to a split
            and/or tag them with a label. {classifiers.length}/{MAX_CLASSIFIERS} used.
          </CardDescription>
          <CardAction>
            <Button size="sm" onClick={openCreate} disabled={atCap}>
              <Plus />
              New classifier
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent>
          {atCap && (
            <p className="text-muted-foreground mb-3 text-xs">
              You've reached the limit of {MAX_CLASSIFIERS} classifiers — delete one to add another.
            </p>
          )}
          {classifiersQuery.isLoading ? (
            <p className="text-muted-foreground text-sm">Loading…</p>
          ) : classifiersQuery.isError ? (
            <p className="text-destructive text-sm">Could not load your classifiers.</p>
          ) : classifiers.length === 0 ? (
            <p className="text-muted-foreground text-sm">No classifiers yet.</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {classifiers.map((classifier) => (
                <li
                  key={classifier.id}
                  className="flex items-start justify-between gap-3 rounded-md border p-3"
                >
                  <button
                    type="button"
                    className="min-w-0 flex-1 text-left"
                    onClick={() => openEdit(classifier)}
                  >
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="truncate text-sm font-medium">{classifier.name}</span>
                      {classifier.targetSplit && (
                        <Badge variant="outline" className="capitalize">
                          {classifier.targetSplit}
                        </Badge>
                      )}
                      {classifier.labelName && <Badge variant="secondary">{classifier.labelName}</Badge>}
                    </div>
                    <p className="text-muted-foreground mt-0.5 line-clamp-2 text-xs">
                      {classifier.prompt}
                    </p>
                  </button>
                  <div className="flex shrink-0 items-center gap-2">
                    <Switch
                      checked={classifier.enabled}
                      onCheckedChange={() => toggleEnabled.mutate(classifier)}
                      aria-label={`${classifier.enabled ? 'Disable' : 'Enable'} ${classifier.name}`}
                    />
                    <Button
                      variant="ghost"
                      size="icon"
                      className="text-muted-foreground hover:text-destructive size-7"
                      aria-label={`Delete ${classifier.name}`}
                      onClick={() => remove.mutate(classifier.id)}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <Dialog open={formOpen} onOpenChange={setFormOpen}>
        <DialogContent>
          <DialogTitle>{editing ? 'Edit classifier' : 'New classifier'}</DialogTitle>
          <DialogDescription>
            Describe what this classifier should match in plain language.
          </DialogDescription>
          <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="clf-name">Name</Label>
              <Input
                id="clf-name"
                value={form.name}
                onChange={(event) => setForm((f) => ({ ...f, name: event.target.value }))}
                placeholder="e.g. Recruiter outreach"
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="clf-prompt">Prompt</Label>
              <Textarea
                id="clf-prompt"
                value={form.prompt}
                onChange={(event) => setForm((f) => ({ ...f, prompt: event.target.value }))}
                placeholder="Cold outreach from a recruiter about a job opportunity."
                className="min-h-24"
              />
            </div>
            <div className="flex gap-3">
              <div className="flex flex-1 flex-col gap-1.5">
                <Label htmlFor="clf-split">Target split</Label>
                <Select
                  value={form.targetSplit || undefined}
                  onValueChange={(value) => setForm((f) => ({ ...f, targetSplit: value as InboxSplit }))}
                >
                  <SelectTrigger id="clf-split" className="w-full">
                    <SelectValue placeholder="None" />
                  </SelectTrigger>
                  <SelectContent>
                    {DEFAULT_SPLITS.map((split) => (
                      <SelectItem key={split.value} value={split.value}>
                        {split.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="flex flex-1 flex-col gap-1.5">
                <Label htmlFor="clf-label">Label name</Label>
                <Input
                  id="clf-label"
                  value={form.labelName}
                  onChange={(event) => setForm((f) => ({ ...f, labelName: event.target.value }))}
                  placeholder="e.g. Recruiting"
                />
              </div>
            </div>
            <div className="flex items-center gap-2">
              <Switch
                id="clf-enabled"
                checked={form.enabled}
                onCheckedChange={(checked) => setForm((f) => ({ ...f, enabled: checked }))}
              />
              <Label htmlFor="clf-enabled">Enabled</Label>
            </div>
            {formError && <p className="text-destructive text-sm">{formError}</p>}
          </div>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setFormOpen(false)}>
              Cancel
            </Button>
            <Button onClick={handleSubmit} disabled={save.isPending}>
              {save.isPending ? <Loader2 className="animate-spin" /> : null}
              {editing ? 'Save' : 'Create'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
